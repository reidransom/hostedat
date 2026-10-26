# hostedat Architecture

## Overview

hostedat is a self-hosted static site hosting platform with a JavaScript worker runtime. It consists of a Go backend (Echo v4 + GORM) and a React/Vite frontend that is embedded into the binary via `web.DistFS`.

**Module path:** `github.com/cryguy/hostedat`

---

## Directory Structure

```
cmd/
  hostedat/         CLI client binary (cobra commands: login, sites, deploy, storage, storage-credentials, version)
  server/           Server entry point
internal/
  api/              HTTP handlers (Echo), middleware, route registration
  auth/             JWT generation/validation, password hashing, API key generation
  certs/            TLS certificate management
  config/           YAML config loading with defaults and validation
  models/           GORM models with nanoid PKs
  seaweedfs/        SeaweedFS IAM client (CreateUser, CreateAccessKey, PutUserPolicy, etc.)
  storage/          Local file storage manager (zip extraction, site paths, bytecode paths)
  worker/           Dual-engine JS runtime (V8/QuickJS), bindings, pool management
web/
  src/              React + Vite frontend
  dist/             Build output (embedded into binary)
```

### Shared secret declarations

`internal/envschema` provides a database-free declaration parser and validator,
shared by environment mutations, deployment/rollback and invocation loading.
Native CLI command integration remains pending.
All declarations describe server-side secrets, including names starting with
`VITE_`; the package never infers public/browser configuration.

`Parse(io.Reader, JSON|YAML)` accepts one flat mapping of variable names to rules.
Input and canonical metadata are limited to 1 MiB. Names use the case-sensitive
ASCII grammar `[A-Za-z_][A-Za-z0-9_]*`. Duplicate names/fields, unknown fields,
grouped/versioned documents, non-string keys, coercion, aliases/anchors, custom
tags, multiple documents, and unsupported types are rejected. YAML uses the
maintained `go.yaml.in/yaml/v3` parser. The normalized `Schema` contains `Rule`
entries with explicit `Required` booleans (defaulting to true when parsed).
`Schema.CanonicalJSON()` emits compact, sorted-key JSON with explicit required
flags. `{}` is an explicit empty declaration; callers must track an absent
manifest separately.

Rules require a `type`: `string`, `number`, `boolean`, `url`, `email`, `json`, or
`enum`. Only enums accept `values`, a nonempty list of distinct exact strings.
There are no defaults, binding values, or client/server sections in declarations.
Validation leaves worker binding values as strings, without trimming or coercion:

- Required values must be present and nonempty. Optional permits absence, not
  invalid supplied values. Optional strings may be empty; optional enums may
  accept empty only if it is an explicit choice.
- Strings retain all whitespace; even whitespace-only strings are nonempty.
- Numbers use JSON numeric syntax and must fit a finite float64. Surrounding
  whitespace, quoted numbers, hexadecimal, NaN, and infinity are rejected.
- Booleans accept exactly lowercase `true` and `false`, with no whitespace.
  The declaration's `required` field likewise accepts only literal `true` or
  `false`, not quoted strings, YAML `yes`/`no`, or capitalized variants.
- URLs must be absolute with a nonempty host, opaque component, or path, with no
  ASCII space/tab/CR/LF. HTTPS, `mailto:`, and `file:///` forms are supported;
  this constraint is not a transport-security or reachability check.
- Email uses Go's mailbox parser but requires an exact address without a display
  name or ASCII space/tab/CR/LF.
- JSON accepts any single JSON value, including scalars and null. JSON syntax
  whitespace is permitted; enum choices match exact strings including whitespace.
- Source declarations, enum choices, and binding values must be valid UTF-8.

`Schema.Validate(map[string]string)` checks all declared names while ignoring
unrelated bindings. Map presence distinguishes absent from explicitly empty.
`Schema.ValidateValue(name, value, present)` checks one declared name without
requiring other entries, for merge-only consumers. `Schema.Check()` validates
programmatically constructed declarations. Diagnostics contain only validated
names and fixed constraints, never input values or raw parser/reader errors.
`ValidateName` checks syntax only: runtime-reserved names and database-backed
binding collisions must be checked separately by later consumers within their
coordinated transaction. Secret classification is also the consuming service's
responsibility; a map of strings alone cannot prove encrypted/secret storage.

#### Local schema and dotenv inputs

`Discover(invocationDir, explicitSchema)` creates a command-scoped `Project`.
It checks only `env.json` and `env.yml` in the invocation directory, never
parents or a deployment output directory. Both defaults require explicit
selection. An explicit path is relative to the invocation directory (absolute
paths also work), must exist, and must have the lowercase `.json` or `.yml`
extension; `.yaml` and executable formats are not supported. The selected
regular file is parsed once. No selection produces a nil schema, distinct from
an explicit empty mapping. Absolute paths and parsed rules remain stable if
the working directory or source declaration changes later.

The root `.env` is beside the selected schema, or in the invocation directory
when there is no schema. A symlinked schema keeps its named directory as the
project root rather than borrowing `.env` from its target. Discovery never
reads dotenv values. `Project.LocalValues(os.LookupEnv)` requires a schema,
reads only the associated `.env` (absence is allowed), and overlays explicitly
present **declared** shell variables, including empty strings. Nil lookup
disables shell overlay. It returns values for `Schema.Validate`; it does not
modify the process environment, write files, start an application, execute
shell code, or use the network.

`ParseDotenv(io.Reader)` accepts at most `MaxDotenvBytes` (1 MiB) across the
whole document, including multiline values and comments. Reads stop at the
limit plus one byte. It requires UTF-8 without NUL and uses the maintained
[`github.com/joho/godotenv`](https://github.com/joho/godotenv) parser (v1.5.1)
behind a strict assignment/duplicate boundary:

- Assignments use `NAME=value`, optionally prefixed with `export` and space/tab.
  Names use the shared grammar. Space/tab around names and `=` is allowed.
  YAML-style `NAME: value`, bare names, and text after a closing quote other
  than space/tab or a comment are rejected.
- LF and CRLF are accepted; CRLF normalizes to LF, including inside multiline
  quoted values. Bare CR is rejected. Single and double quotes may span
  physical lines. A quote immediately after a backslash is treated as escaped,
  matching the pinned parser, even after an even-length backslash run.
- A full-line `#` starts a comment. In unquoted values, the first `#` at value
  start or after space/tab starts a comment; other `#` characters are literal.
  Quoted `#` characters are always literal.
- Unquoted surrounding whitespace is trimmed using the parser's space, tab,
  vertical-tab, form-feed, NEL, and nonbreaking-space set; internal whitespace
  is retained. Quotes preserve surrounding and internal whitespace. Empty
  assignments and empty quotes produce an explicitly present empty string.
- Single quotes preserve backslashes literally. Double quotes decode `\n` and
  `\r`; other backslash escapes remove the backslash (for example, `\t` becomes
  `t`, `\"` becomes `"`, and `\\` becomes `\`). These are dotenv parser
  semantics, not shell or JSON escape rules.
- Dollar references (`$NAME`, `${NAME}`, `$(...)`) and backticks are always
  literal: no variable, command, or shell expansion occurs. The wrapper shields
  dollars from the parser's built-in interpolation using a forbidden-input
  NUL sentinel and restores them afterward; it never calls a load/overload or
  command-execution API.
- Duplicate names, including `export` aliases and empty first assignments, fail
  before parser overwrite. Assignment-looking text inside multiline quotes is
  part of the value, not a new assignment. Errors contain only fixed constraints
  and validated names, never input lines, values, paths, or raw reader errors.

`Project.ReadSyncSource(path)` requires a named regular file, relative to the
invocation directory unless absolute, and never applies shell overrides. It
rejects the project's local `.env` by cleaned path and opened-file identity,
covering file/directory symlinks and hard links as well as the target of a
symlinked `.env`. Neither the source nor its path is written or saved.
Callers must separately validate supplied schema values and binding collisions
before a remote mutation; these helpers do not implement sync, API requests,
CLI commands, deployment enforcement, or database-aware collision checks.



---

## Request Routing

### Site Traffic (subdomain routing)

Incoming HTTP requests are routed based on the `Host` header:

1. `example.com` ↁEAPI server + dashboard (Echo router, bare domain)
2. `storage.example.com` ↁES3 reverse proxy (proxies to SeaweedFS/MinIO)
3. `<slug>.example.com` ↁEStatic site serving or worker execution

For subdomain traffic:
- The subdomain slug is extracted from the host
- The site's `SiteRulesCache` is consulted (TTL 5min, max 1000 entries, bounded LRU)
- If the site `HasWorker` and has an active deployment, the request goes to the worker engine
- Otherwise, static files are served from `{storagePath}/{siteID}/{deployID}/`

Static serving features:
- `_redirects` file: supports `301`, `302` (redirect), and `200` (rewrite/proxy rules) with wildcard patterns and `:splat` captures
- `_headers` file: custom response headers per path pattern
- Custom `404.html` if present
- SPA mode: all 404s fall back to `index.html`
- Shared `internal/artifactpolicy` blocks dotenv path components and reserved
  environment metadata case-insensitively before worker/static dispatch and at
  resolved-file lookup, including rewrite targets and physical symlink aliases
  in old deployments. Blocked requests do not become SPA responses; ordinary
  missing paths retain SPA behavior. `env.ASSETS.fetch` uses the same policy.

### API Traffic

All API routes are under `/api/v1`. The CLI sends an `X-Hostedat-Version` header; the server rejects versions below `min_cli_version` with `426 Upgrade Required`.

The public version response also advertises `capabilities: ["secrets-env-v1"]`.
`internal/client/env.go` negotiates this exact contract before mutations,
candidate validation and manifest-bearing `DeployArchive` uploads. Bulk uses one
request with no sequential fallback. Typed DTOs omit secret values even when
decoding a nonconforming response, while retaining empty plaintext strings.
HTTPS is mandatory for environment operations unless the caller explicitly
opts into loopback-only HTTP development. The shared client refuses all
redirects without changing the caller's HTTP transport/timeout. Secure operation
errors preserve status, never arbitrary response bodies or transport diagnostics.
Schema-less deployment compatibility is unchanged. Native CLI commands and
schema-aware source packaging are separate consumer integrations.

---

## Authentication

### JWT Tokens

- Generated via `auth.GenerateToken(userID, email, role, secret)`
- 24-hour expiry
- Claims include: `user_id`, `email`, `role`
- Validated on every request; user is reloaded from DB to catch role changes and deletions
- Logout: token SHA-256 hash stored in `RevokedToken` table until natural expiry

### API Keys

- Format: `hd_` prefix followed by random bytes
- Stored as SHA-256 hash in `APIKey.KeyHash`
- Identified by `strings.HasPrefix(token, "hd_")`
- `last_used_at` updated on each use

### CLI Auth Flow (PKCE)

1. CLI opens browser to `GET /auth/cli?port=PORT&state=STATE&code_challenge=CHALLENGE&code_challenge_method=S256`
2. User logs in via the HTML form
3. Server validates credentials, creates a one-time `AuthCode` (60s expiry) with the PKCE challenge
4. Browser redirects to `http://localhost:PORT/callback?code=CODE&state=STATE`
5. CLI's local server receives the code, calls `POST /auth/token` with code + verifier
6. Server verifies PKCE challenge, marks code as used, returns JWT
7. CLI exchanges JWT for an API key (stored in `~/.hostedat/config.json`)

---

## Database

**GORM** with SQLite, currently the only configured driver. Connections use WAL
and a 5-second busy timeout, including newly opened pooled connections.

### ID Generation

All primary keys use **nanoid**: 12 characters from `[0-9a-z]` alphabet.

```go
const nanoidAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
const nanoidLength = 12
```

### Models

| Model | Key Fields |
|-------|-----------|
| `User` | `id`, `email`, `password_hash`, `role` (`superadmin`/`admin`/`user`) |
| `Site` | `id`, `user_id`, `subdomain_slug` (unique), `name`, `spa_mode`, `has_worker`, `active_version`, `active_deploy_id`, internal `env_declarations_enforced` |
| `Deployment` | `id`, `site_id`, `version` (auto-increment per site), `file_hash`, `has_worker`, nullable internal `env_declaration` |
| `APIKey` | `id`, `user_id`, `key_hash`, `name` |
| `Invite` | `id`, `code`, `created_by`, `max_uses`, `use_count`, `expires_at`, `active` |
| `AuthCode` | `id`, `code`, `user_id`, `code_challenge`, `used`, `expires_at` |
| `WorkerEnvVar` | `id`, `site_id`, `name`, `value` (plain only), `secret`, `secret_version`, `secret_key_id`, `secret_nonce`, `ciphertext` |
| `KVNamespace` | `id`, `site_id`, `name` |
| `KVEntry` | `id`, `namespace_id`, `key`, `value`, `metadata`, `expires_at` |
| `CronSchedule` | `id`, `site_id`, `cron`, `enabled`, `last_run_at` |
| `WorkerLog` | `id`, `site_id`, `level`, `message`, `created_at` |
| `StorageBucket` | `id`, `site_id`, `name` (binding), `bucket_name` (S3), `public` |
| `S3Credential` | `id`, `user_id`, `external_key_id` (IAM username), `access_key_id`, `name` |
| `CacheEntry` | `id`, `cache_name`, `url`, `status`, `headers`, `body`, `expires_at` |
| `RevokedToken` | `token_hash`, `expires_at` |
| `Setting` | `key`, `value` (used for `registration_enabled`, `invite_required`) |

### Site binding and activation transactions

Environment upsert/delete, deployment activation, rollback, and KV/D1/durable
object/storage binding creation share `models.WithSiteTransaction`. Its first
statement is a no-op write to the site row, acquiring SQLite's writer lock before
reading the current site or validating/changing bindings. Checks and writes must
use the callback's transaction handle. Errors roll back all database writes;
external work and post-commit audits must stay outside the callback.

This is database-backed coordination across connections/processes, not a Go
mutex. SQLite serializes writers database-wide, including unrelated sites.
Lock acquisition can fail after the busy timeout; it must not continue with
uncoordinated writes. Deployment versions are allocated under the same lock,
and rollback reloads current activation state after acquiring it.

`env_declaration IS NULL` means absent, while canonical `"{}"` means an explicit
empty declaration. The site's default-off enforcement flag becomes true on its
first successful manifest-bearing activation; later schema-less deploys and
rollbacks are rejected. Empty declarations may remove requirements without
disabling enforcement. Internal fields are not serialized by the management API.
Activation validates current decrypted values, classification, required presence
and namespace collisions both before compilation and inside the site transaction.
Rollback validates its target against current values, never historical secrets.
Failed activation retires staged assets/bytecode and compiled pools.

`environment.Service.Declaration` retains only the latest loaded immutable
snapshot per site and never caches values. Invocation loading validates the
exact deployment selected for execution, with fresh decrypted values and binding
metadata from one read transaction. Binding adapters retain the live database
handle, not the closed snapshot transaction. Cron and dispatcher factories take
both site and deployment IDs to avoid validating a different active artifact
during concurrent deployment changes.

Startup refuses duplicate environment `(site_id, name)` rows. A name lookup
also refuses ambiguity rather than selecting a first/last row. To resolve legacy
data, stop all writers and workers and back up the database. Inspect only
`id, site_id, name, secret` metadata to identify duplicate groups, explicitly
choose which IDs to retain/remove, and resolve them in a transaction; no
automatic winner or value-based deduplication is supported.

Offline encrypted-storage maintenance calls
`models.InstallWorkerEnvUniqueness(tx)` after explicit resolution, inside its
maintenance transaction. It rechecks duplicates and installs the unique
`idx_worker_env_site_name` index on `(site_id, name)`, refusing partial resolution.
The index is intentionally not installed by automatic model migration.

### Encrypted environment storage

`internal/environment.Service` owns single upsert, metadata list/delete and
invocation snapshot loading. Mutations use the same site transaction as activation.
Secret rows have empty `Value`, format version 1, a key ID, a random 96-bit nonce
and AES-256-GCM authenticated ciphertext. Associated data binds site ID, exact
variable name and format version. Plain bindings have no encryption metadata.
Management DTOs never serialize encrypted fields. List/delete do not decrypt.
Omitted write values fail, while omitted classification preserves the old flag.

Startup loads the operator-provisioned key file, checks uniqueness is installed,
and authenticates every secret before starting traffic or cron. Legacy plaintext,
unknown keys, invalid formats, tampering and cross-binding ciphertext reuse fail
closed. SQL logging parameterizes query values, including failed writes.
`BuildEnvFromDB` returns `(*worker.Env, error)`; HTTP does not execute on failure,
cron skips the run without changing `last_run_at`, and dispatcher factories return
errors. Each load decrypts into a new in-memory snapshot. Existing invocations and
WebSockets retain their snapshot; updates affect the next load.

See the Configuration Reference's offline migration, rotation and recovery
procedure. Keys and databases require separate backups. Encryption does not erase
old SQLite WAL/free pages/backups, protect against administrators with keys, stop
authorized workers logging values, or guarantee memory zeroization.


### Site Deletion Cascade

Deleting a site transactionally removes: `KVEntry` (via namespace IDs), `Deployment`, `WorkerEnvVar`, `KVNamespace`, `CronSchedule`, `WorkerLog`, `StorageBucket`, then the site itself. After the transaction: local files deleted, S3 buckets removed, IAM policies reconciled.

---

## Authorization Pattern

Every handler that operates on a site does an inline ownership check:

```go
if site.UserID != userID && role != "admin" && role != "superadmin" {
    return errorJSON(c, http.StatusForbidden, "access denied")
}
```

The `RequireSiteOwner(db)` middleware is available but handlers perform inline checks directly. Admin routes use the `RequireAdmin()` middleware which checks for `superadmin` or `admin` role.

---

## Deploy Pipeline

1. **Upload**: Client POSTs a zip as `multipart/form-data` with field `file`
2. **Validate**: File size checked against `storage.MaxZipSize`; hash computed (SHA-256)
3. **Pre-generate ID**: A new nanoid `deployID` is generated before any DB writes
4. **Preflight and extract**: Validate every original ZIP path before wrapper
   flattening, then validate normalized destinations, duplicates and file/directory
   conflicts before writing. Reject dotenv components case-insensitively,
   traversal/NUL/absolute/drive/UNC/backslash paths, symlinks, unsupported types
   and archive bounds. Parse only artifact-root `_hostedat.env.json` with
   `envschema`; `ExtractZip` returns `(*string, error)` containing canonical
   metadata separately and never writes it as an asset. Extract other files to
   `{storagePath}/{siteID}/{deployID}/`; cleanup runs on extraction failure.
5. **Worker detection**: If `_worker.js` present:
   - Size checked against `MaxScriptSizeKB` (default 1024 KB)
   - Validated and cached via `Engine.CompileAndCache()`
6. **DB transaction**: `Deployment` record (including nullable canonical
   `EnvDeclaration`) created, `Site.active_version` and `Site.active_deploy_id`
   updated atomically. Requirement validation/enforcement during activation and
   rollback is a pending cutover, not implied by metadata parsing.
7. **Pool invalidation**: Old worker pool for the previous deploy is invalidated

**Rollback** works by updating `Site.active_version` and `Site.active_deploy_id` to point at a previous deployment. The source for that deployment is already on disk.

---

## Worker Runtime

### Runtime Stack

Two JavaScript engines are supported (selected at build time via build tags):

- **V8** (recommended, `-tags v8`): JIT-compiled via `github.com/tommie/v8go` CGO bindings. Best performance. Linux and macOS only.
- **QuickJS** (default): Interpreted via `modernc.org/quickjs` (pure Go). Runs on all platforms including Windows. No CGO required.

Both engines share the same Workers API, pool management, and execution flow.

- Isolate/context pool with per-worker sandboxed execution
- Script validation happens at deploy time; execution uses pre-warmed runtimes

### Pool Management

Each deployed worker site gets a pool of pre-warmed runtimes:

```go
type poolKey struct {
    SiteID    string
    DeployKey string  // = deployID of the active deployment
}
```

- Pools stored in `Engine.pools` (`sync.Map`)
- Pool size configurable (default 4 runtimes per site)
- On request: checkout a runtime from the pool, execute, return
- Pool invalidation: when a new deployment is pushed, the old pool is marked invalid; a fresh pool is created lazily for the new `deployKey`
- On server restart: runtimes are recreated lazily on first request; source code is re-parsed from disk

### Execution Flow

1. Request arrives for `<slug>.example.com`
2. Site rules looked up (cache ↁEDB)
3. Worker pool for `{siteID, activeDeployID}` checked out
4. JS `fetch` handler called: `export default { fetch(request, env, ctx) }`
5. `env` object built with: plain vars, secret vars, KV namespaces, D1 databases, Durable Objects, Queues, Service Bindings, storage buckets, `ASSETS` binding
6. Response extracted from JS, converted to Go `WorkerResponse`
7. Runtime returned to pool

### SSRF Protection

`fetch()` in workers blocks requests to private/loopback IP ranges:
- `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`
- `127.0.0.0/8`, `::1/128`
- `169.254.0.0/16` (link-local)
- Any other RFC-1918 / loopback range

Both the initial hostname resolution and redirect destinations are checked. Max 10 redirects.

---

## Object Storage

Object storage is optional and must be enabled in config (`object_storage.enabled: true`).

### Modes

**Managed:** hostedat starts and manages a SeaweedFS instance internally (`weed` binary). Data stored in `object_storage.data_dir`.

**External:** Connects to an existing S3-compatible endpoint (`object_storage.s3_endpoint`). Credentials must be provided.

### S3 Proxy

Traffic to `storage.<domain>` is proxied to the SeaweedFS/MinIO endpoint. The proxy handles SigV4 request signing and forwards to the internal endpoint.

### Bucket Naming

Bucket names must start with the site ID followed by a hyphen (e.g., `abc123def456-images`). This enforces namespace isolation. The full pattern: `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`, 3-63 chars.

### IAM Credentials

Per-user S3 credentials are managed via the SeaweedFS IAM API:
- IAM username format: `hd-{userID}-{credentialName}`
- On credential creation: IAM user created, access key generated, policy attached
- Policy grants `s3:*` on all buckets owned by the user
- Policy reconciled when buckets are created or deleted
- On user/site deletion: IAM keys and users revoked

### Worker Storage Binding (R2-compatible)

Buckets are exposed to workers as R2-compatible objects on `env.BINDING_NAME`:

```js
// get
const obj = await env.IMAGES.get("photo.jpg");
// put
await env.IMAGES.put("photo.jpg", body);
// delete
await env.IMAGES.delete("photo.jpg");
// head
const meta = await env.IMAGES.head("photo.jpg");
// list
const list = await env.IMAGES.list({ prefix: "photos/", limit: 100 });
// presigned URL
const url = await env.IMAGES.createSignedUrl("photo.jpg", { expiresIn: 3600 });
// public URL (only for public buckets)
const url = env.IMAGES.publicUrl("photo.jpg");
```

---

## Configuration

Config is loaded from a YAML file (path passed as CLI arg). Key settings:

| Key | Default | Description |
|-----|---------|-------------|
| `domain` | required | Base domain (e.g. `example.com`) |
| `listen` | `:8080` | HTTP listen address |
| `storage_path` | `./data/sites` | Local file storage root |
| `jwt_secret` | required (32+ chars) | HMAC secret for JWTs |
| `min_cli_version` |  E| Minimum accepted CLI version (semver) |
| `database.driver` | `sqlite` | Only supported configured driver |
| `database.dsn` | required | Database connection string |
| `secrets.key_file` | required | Owner-only operator key-file path; no implicit generation |
| `worker.pool_size` | `4` | Runtimes per site pool |
| `worker.memory_limit_mb` | `128` | Per-runtime memory limit |
| `worker.execution_timeout` | `30000` (ms) | Per-request JS timeout |
| `worker.max_fetch_requests` | `50` | Max `fetch()` calls per request |
| `worker.fetch_timeout_sec` | `10` | Per-fetch HTTP timeout |
| `worker.max_response_bytes` | `10485760` (10 MB) | Max fetch response body size |
| `worker.max_log_retention` | `7` (days) | Worker log retention |
| `worker.max_script_size_kb` | `1024` | Max `_worker.js` size |
| `worker.data_dir` | | Directory for worker data (D1 databases, Durable Objects, Queues) |
| `object_storage.enabled` | `false` | Enable S3-compatible object storage |
| `object_storage.managed` | `false` | Auto-manage SeaweedFS instance |
| `object_storage.s3_endpoint` | `http://127.0.0.1:8333` | S3 API endpoint |
| `object_storage.region` | `us-east-1` | S3 region |

---

## Caching

`SiteRulesCache` stores parsed redirect/header rules per site:
- Bounded: max 1000 entries
- TTL: 5 minutes
- Populated on first request, invalidated on TTL expiry
- Does not need manual invalidation on deploy (TTL handles staleness)

---

## Site Content Features

### `_redirects`

One rule per line: `<from> <to> <status>`

```
/old-path /new-path 301
/api/* https://api.external.com/:splat 200
/* /index.html 200
```

- `301`/`302`: HTTP redirect
- `200`: Internal rewrite (serves the target path without redirecting)
- Wildcards (`*`) and `:splat` captures supported

### `_headers`

```
/static/*
  Cache-Control: public, max-age=31536000
  X-Content-Type-Options: nosniff

/*
  X-Frame-Options: DENY
```

### SPA Mode

When `spa_mode: true`, all 404s serve `index.html` with status 200, enabling client-side routing.

### Custom 404

If `404.html` exists in the site root, it is served for not-found responses.

---

## CLI Client

The CLI binary (`hostedat`) communicates with the server API using API keys. Configuration stored at `~/.hostedat/config.json`.

**Auth resolution order:**
1. `--api-key` flag
2. `HOSTEDAT_API_KEY` environment variable
3. `~/.hostedat/config.json`

**Server resolution order:**
1. `--server` flag
2. `HOSTEDAT_SERVER` environment variable
3. `~/.hostedat/config.json`
4. Compiled-in default (set via `-ldflags`)

**Site resolution (CLI):** The CLI resolves site arguments (name, subdomain slug, or ID) by calling `ResolveSiteID()` which searches across all three identifiers. This is a CLI-only convenience; API endpoints accept only site IDs.

**Commands:** `login`, `sites list/create/delete`, `deploy`, `storage list/create/update/delete/upload`, `storage-credentials list/create/delete`, `version`
