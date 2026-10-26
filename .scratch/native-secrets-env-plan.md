# Native hostedat secrets and environment workflow

This is an implementation plan. No platform code, site settings, or secret values have changed.

## Goal

Give hostedat checked-in environment declarations, explicit secret writes to a selected target, and deployment checks. Every declared variable is a server-side secret. Keep the upload-based deployment model and Go/Cobra CLI. Do not build applications, inject browser values, or require a Colorfly wrapper, Node, or executable TypeScript to manage secrets.

References:
- https://void.cloud/guide/deployment#environment-variables
- https://void.cloud/guide/env-vars
- Local contracts: `docs/public/api-reference.md`, `docs/public/cli-reference.md`, `docs/public/worker-runtime.md`.

Void is a workflow reference, not the source of this file format. The flat JSON/YAML declarations below are specific to this hostedat plan.

## Verified baseline

- `cmd/hostedat/main.go:32-55` registers Cobra commands. There is no native `secret` or `env` command. `deployCmd` resolves a site and calls the client uploader without building the application. See lines `197-241`.
- CLI authentication uses environment variables, flags, and `~/.hostedat/config.json`. Keep that resolution and the existing handling of ambiguous site references. Do not add another credential store or saved deployment target. See `main.go:610-685` and `internal/client/sites.go:42-77`.
- `internal/client/deploy.go:58-110` already excludes `.env`, `.env.*`, VCS/cache directories, and symlinks, regardless of `.gitignore`. Keep those exclusions. Dotenv exclusion is not a missing CLI feature.
- `internal/api/worker_handlers.go:22-103` upserts one name per POST and stores the submitted value in `WorkerEnvVar.Value`. Only GET/list masks secret values. The write response returns the full model, secret value included. Deletion uses both site and variable ID. See lines `106-131`.
- `internal/api/deploy_handlers.go:81-168` extracts the archive and compiles the worker. A transaction then inserts the deployment and makes it active. Check required secrets before activation, including a final check in that transaction. A check after reporting success is too late.
- `internal/models/models.go:157-173` and `internal/models/database.go:14-69` show plaintext secret storage in SQLite and no unique site/name index. `internal/workeradapter/env.go:22-51` builds Vars/Secrets from those rows and ignores database errors.
- Production HTTP and cron load the live site environment. See `internal/api/site_serving.go:381-388`, `cmd/server/main.go:173-188`, and `internal/workeradapter/cron.go:61-119`. The dispatcher is not wired into production. Queue adapters do not prove that a queue consumer runs.
- `web/src/lib/components/sites/worker/WorkerPanel.svelte:39-68` hides environment setup until a worker exists. That would block a first deployment that requires secrets. `EnvVars.svelte` ignores the POST value but defaults new and upserted entries to plaintext. `web/src/lib/api/types.ts:66-72` requires a value field.
- `internal/storage/storage.go:27-170` rejects traversal, symlinks, unsupported file types, and oversized ZIP contents. It accepts dotenv files, strips a common wrapper directory, and lets repeated destinations overwrite earlier files. Validation must check both original and final archive paths.
- This checkout was checked with `go run ./cmd/hostedat --help`. It lists deploy, login, sites, storage, storage-credentials, and version. That verifies the baseline, not the planned behavior.
- No LSP server is configured for this project. Enable Go navigation during implementation if available. Inspect references before changing exported interfaces.

## Decisions and boundaries

1. Local values stay in an ignored root `.env`. Only `env check` may read that file. Secret commands and deploy must not load or upload it. Application development servers handle their own local dotenv loading.
2. Production writes use explicit `hostedat secret` commands. Put changes one name. Sync merges the supplied names in one transaction and leaves omitted names untouched. It never replaces the site's entire environment or prunes entries.
3. Use declarative `env.json` or `env.yml`, not executable `env.ts`. Both formats contain a flat mapping of environment names to validation rules, without version or server/client sections. The Go CLI must not run project JavaScript to discover required names.
4. Select sites as the current CLI does, with `hostedat secret put <site> <name>`. Reuse `ResolveSite` for IDs, names, subdomains, and ambiguous references. Secrets must not require project linking.
5. Every declared variable is a server-side secret in the selected target environment, even a configuration string such as a model ID. There is no client classification or public-prefix inference. Keep untyped/plain bindings outside declared names working.
6. Encrypt stored secrets and remove their values from management responses before claiming Void-like protection. Masking is not encryption. Encryption protects database contents and backups, not against an administrator with the key or worker code authorized to read a secret.
7. Do not add secret read/export, automatic `.env` sync, CLI arguments containing values, client builds, browser-value injection, bundler integration, generated TypeScript declarations, or automatic application builds. Colorfly's browser and API behavior stay unchanged.

## User-facing contract

```sh
# Local validation: root .env plus shell overrides, no network.
hostedat env check

# Production writes: prompt without echo or accept stdin for CI.
hostedat secret put colorfly OPENCODE_GO_API_KEY
printf '%s' "$OPENCODE_GO_API_KEY" | hostedat secret put colorfly OPENCODE_GO_API_KEY --stdin

# Explicit dotenv-formatted input for the selected target's server-side secrets.
hostedat secret sync colorfly /secure/production-secrets.txt

# Names/metadata only; no secret values.
hostedat secret list colorfly
hostedat secret delete colorfly OPENCODE_GO_API_KEY

# Read-only production validation; does not send local .env values.
hostedat env check --remote --site colorfly

# Deploy dist using the schema in the invocation directory.
hostedat deploy colorfly ./dist
```

- Add `--schema <path>` to put, sync, check, and deploy. Look for `./env.json` and `./env.yml` in the invocation directory. Use the one present. If both exist, require `--schema` rather than choosing or merging them. Use `--schema` for a different filename or a project that already uses these names for something else. Select JSON for `.json` and YAML for `.yml`; reject unsupported extensions. Do not search parent directories. Resolve the file once per command. If neither default exists, legacy deployments remain unchanged. An explicitly requested missing file is an error. Put and sync use the schema when present. Without one, they still check names, reserved bindings, and any active remote schema.
- Interactive put hides terminal input and uses Cobra's stdin/stderr, not hard-coded process streams. Non-interactive callers must choose `--stdin`, so CI cannot hang at a prompt. Bound the input size. Remove one trailing CRLF or LF delimiter and preserve all other whitespace, including multiline content. Do not trim credentials. Reject empty required values without echoing them.
- Do not add `--value`. The CLI must not encourage putting values in shell history or process listings. `--stdin` never loads `.env`.
- Sync requires a named file. Reject the project's local `.env` to prevent accidental uploads of local values. Recommend a source outside the deployment tree with `0600` permissions. Do not archive it or save its path or contents in site configuration.
- Use a maintained dotenv parser and document its quote, comment, CRLF, and multiline behavior. Test `#` inside quoted URLs and keys. Never execute shell substitutions. Validate the entire input before any request. Reject duplicate names, invalid syntax, binding collisions, invalid declared values, and undeclared names when a schema is present. With a schema, sync writes only supplied, declared variables as server-side secrets to the selected target. Omitted variables and unrelated target bindings stay untouched. Share input-size limits with the server.
- Resolve the server and site before prompting. Show the target without sensitive data. Success messages contain only the operation, names, and counts. Never print request or response bodies, plaintext, ciphertext, raw parser lines, or validation errors containing values.
- List only secret entries, sorted by name, with IDs and secret flags. `--json` also returns metadata only. Delete resolves a name within the site and refuses plaintext bindings. Follow existing confirmation behavior and support `--yes` for CI. Reject deletion or plaintext downgrade of names required by the active schema.

## Declaration format and validation

Example committed `env.json`:

```json
{
  "OPENCODE_GO_API_KEY": { "type": "string", "required": true },
  "OPENCODE_GO_MODEL": { "type": "string", "required": false }
}
```

The same declaration in `env.yml`:

```yaml
OPENCODE_GO_API_KEY:
  type: string
  required: true
OPENCODE_GO_MODEL:
  type: string
  required: false
```

- Put JSON and YAML parsing and the shared validator in `internal/envschema`. Both parsers produce the same declaration model. CLI, API, deployment, and runtime checks must all use it. Use a maintained YAML parser. Accept one document with string mapping keys, reject duplicate keys before decoding can discard them, and enforce schema field types without string coercion. Apply the same unknown-field, size, and value-free diagnostic rules to both formats.
- The document root is a flat mapping of environment names to validation rules. There is no version field or server/client grouping. Reject unknown rule fields, duplicate names or keys, and oversized documents. Document the environment-name grammar. Check runtime binding names and site-bound namespace collisions, not only a static prefix list.
- Support string, number, boolean, url, email, JSON, and string enums with explicit `values`. Validate without coercing values. Worker `env` values remain strings. Numbers must be finite, boolean spellings must be documented exactly, and URLs must be absolute. Diagnostics name the variable and constraint. Never include the value or a nested parser error that contains it.
- Default `required` to true. Required strings cannot be empty. Optional allows absence, not invalid input. Preserve significant whitespace in stored values.
- The schema declares server-side secret requirements, not values or runtime defaults. Do not add secret defaults, example credentials, or a typed `void/env` replacement. Values come only from explicit put input or a named sync source. Declarations never supply browser configuration or trigger an application build.
- Treat names uniformly, including names beginning with `VITE_`. A prefix does not make a declared variable public or authorize sending it to browser code. All declared values remain target-side secrets.
- Local `env check` reads the root `.env` associated with the schema. Explicitly set shell variables override it, including empty strings. Check the declared variables without changing the parent environment, starting the app, writing files, contacting the server, or printing values. `env check --remote` checks only the target's stored secrets on the server. It neither reads build-shell values nor uploads local `.env` values.

## Server/API design

Keep the per-site routes and single-name writes. Add batch and validation operations. Sync must not become a sequence of POSTs that can leave half the input applied.

| Endpoint | Planned behavior |
| --- | --- |
| `POST /api/v1/sites/:id/worker/env` | Upsert one name through the shared environment service. Omit secret values from the response DTO. |
| `GET /api/v1/sites/:id/worker/env` | Return plain values but omit `value` for secrets. Use an explicit DTO. Never serialize encrypted database models. |
| `DELETE /api/v1/sites/:id/worker/env/:varId` | Keep ownership and site scoping. Enforce the active declaration. |
| `POST /api/v1/sites/:id/worker/env/bulk` | Accept a bounded array of `{name,value,secret}` entries and an optional declaration document. Validate all entries and commit in one transaction. Return metadata only. |
| `POST /api/v1/sites/:id/worker/env/validate` | Accept only a candidate declaration. Check stored secrets for presence, classification, and valid decrypted values on the server. Return names and reasons only. No writes or local values. |

- Use one small environment service for single writes, bulk writes, deletion, validation, and runtime loading. Do not create a second secret store. Keep name-based upserts. Detect and resolve existing duplicates before adding a unique database constraint on `(site_id, name)`.
- Keep owner/admin authorization on every route. IDs and names from different sites must not bypass site scoping. Check body limits and the whole batch before changing data. Reject empty, duplicate, or oversized batches without writes. Distinguish omitted `value` from an explicit empty string. Omission is invalid. Empty strings follow the declaration rules. An omitted `secret` must not downgrade an existing secret.
- Put and sync send `secret: true`. A supplied local schema checks the submitted values but does not become active. Also enforce the active schema. Staging a value for a later deployment must not break the running application. The existing API may still accept unknown legacy names unless they collide with reserved bindings.
- Reuse the API client's auth, error, and HTTP helpers. Add typed environment methods in `internal/client/env.go`. Keep secrets out of URLs and logs. Remove values from server errors before relying on the client's existing `APIError.Message` display.
- Omit secret values from single, list, and bulk responses, including successful creates and updates. Public DTOs must retain valid empty values. Change the dashboard type to a public/secret discriminated union. Keep its visual placeholder local to the UI and never submit it as a replacement value. List and delete can read metadata without decrypting secrets. Set `Cache-Control: no-store` on management responses.
- Audit only names, counts, and actions. Keep the existing best-effort policy. Emit success records after a mutation or batch commits, never while it could still roll back. Do not add guaranteed-audit behavior. Parameterize SQL logging. Keep values out of request traces, SQL diagnostics, and crash messages.

## Encrypted storage and migration

- Store plaintext only in `secret: false` rows. Secret rows need versioned ciphertext, a key ID, and a unique random nonce. Use standard-library AES-256-GCM with associated data that binds ciphertext to site ID, variable name, and format version. Authentication failure is an error. Never interpret failed ciphertext as plaintext.
- Load the encryption key from an operator-provisioned key file with restrictive permissions. Keep it separate from the database and deployment archives. Do not derive it from JWT/auth credentials, put it in YAML examples, log it, or silently generate a replacement on restart. Validate it at startup and document backup and restore requirements.
- Require an operator-run migration before starting the new server. Stop writes and workers. Back up the database and key separately. In one transaction, encrypt every legacy `secret: true` row and clear its plaintext field. Verify counts and decryption without logging contents, then start the new binary. Refuse startup while duplicate names, unknown key IDs, legacy plaintext secrets, or invalid ciphertext remain. The permanent runtime must not fall back to reading plaintext secrets.
- Migration does not erase plaintext from old backups, SQLite WAL/free pages, replicas, or exported databases. Document secure retirement and compaction for the actual database. Updating a field does not remove every old copy.
- Rotate keys during explicit maintenance. Supply old and new keys, re-encrypt in a transaction while stopped, verify, then switch the configured active key. Keep the old key only as long as old backups require it. Fail on missing or wrong keys before serving traffic. Returning to a binary that expects plaintext requires a coordinated database restore, not an undocumented downgrade.
- Decrypt only in memory when building worker bindings. Propagate decryption and schema-validation errors through every environment-building caller, including callers other than HTTP fetch. Do not omit a required secret or report a successful invocation with a broken environment.
- Change `BuildEnvFromDB` to return `(*worker.Env, error)`. Update `BuildEnvOptions`, HTTP `WorkerDeps`/`buildWorkerEnv`, startup's cron environment factory, cron invocation handling, and the dispatcher factory interface and tests. On failure, HTTP returns a generic configuration error without executing the worker. Cron skips the invocation and must not report success. Secret changes affect the next environment build, not an already-running request or WebSocket.
- Require HTTPS for remote secret writes. Allow HTTP only for explicit loopback development. The CLI must not follow a write redirect that could send credentials or values to another origin. At-rest encryption cannot stop a worker from logging or returning a secret. It does not guarantee memory zeroization or revoke plaintext a worker already received.

## Deployment and runtime enforcement

- Package the canonical schema as reserved metadata at `_hostedat.env.json` in the upload. The CLI parses the selected JSON or YAML source and serializes the shared flat declaration model as JSON in memory, even when deploying `dist`. API requests and stored snapshots also use this JSON representation. Exclude the selected source declaration from normal packaging so the artifact contains one canonical declaration. Reject a conflicting, separately supplied canonical manifest instead of choosing one. Include declarations only, never dotenv contents or secret values. Deployment does not sync values or run an application build.
- Dashboard and raw API ZIPs use the same artifact-root `_hostedat.env.json` after supported wrapper-directory flattening. Do not accept another alias or a multipart declaration. Validate and persist the manifest, then remove it from static assets. Deny resolved static requests for reserved environment metadata and historic dotenv artifacts. Cover `env.ASSETS.fetch`, rewritten targets, SPA/custom-domain paths, and normalized aliases.
- Keep the CLI's dotenv and symlink exclusions and the server's symlink, type, and size checks. Reject dotenv files on the server too, so direct uploads cannot bypass the rule. Share a case-insensitive dotenv path-component policy between client and server for portability. Document that this tightens the CLI's current case-sensitive behavior. Before writing any files, check every original ZIP path component and every destination after wrapper flattening. Reject traversal, NULs, absolute/drive/UNC paths, backslashes, and duplicate normalized destinations. Stripping a `.env` wrapper must not make `.env/visible.txt` acceptable. Keep partial-extraction cleanup. This policy checks filenames. It cannot detect secrets renamed or embedded in bundles.
- Add a nullable canonical declaration snapshot to `Deployment` and an enforced-declarations flag to `Site`. The first successful deployment with a manifest sets the flag in the activation transaction. Later deployments must include a manifest. Legacy sites that have not opted in may still deploy without a schema. An authorized new manifest may revise or remove requirements, but do not add a missing-secret bypass flag. Parse schemas once per deployment or loaded snapshot, not from disk on each request.
- Run a read-only client preflight for diagnostics. The server makes the final decision. In the activation transaction, revalidate stored secrets, their types, and their classification before changing `ActiveDeployID`. If required values are missing or invalid, leave the previous deployment active and clean up staged assets and compiled artifacts.
- Serialize same-site environment changes and deployment/rollback activation through database-backed coordination. A process-local mutex cannot close the check/write race across server processes. Deletes and updates must respect the schema active when their transaction commits.
- Before rollback activation, check the selected deployment's declaration against current secrets. Never restore old secret values. Reject a legacy schema-less rollback on an enforced site unless that artifact has an explicit supported declaration. Rollback must not disable the site's requirements.
- Check stored and decrypted bindings against the deployment declaration when building an invocation environment. Keep the worker's existing `env` access pattern. Do not expose secret-management endpoints to browser code. The next invocation must see environment changes without a redeploy or stale values from an unmanaged cache.
- Check namespace and binding collisions both when writing environment values and when creating bindings. ASSETS, KV, D1, storage, durable objects, and other bindings must not shadow declared secret names.
- Require server support before secret mutations or declaration-bearing uploads. Advertise a versioned secret/environment capability on the existing version endpoint. If it is absent, fail with an upgrade message. An older server must not accept a manifest as public static content or present plaintext storage as the new encrypted behavior. Bulk sync must never fall back to sequential per-name POSTs.

## Dashboard and compatibility

- Show the environment editor for owned sites before `hasWorker` becomes true. Users must be able to provision secrets before the first deployment that requires them.
- Keep list and delete interactions working as DTO and client types change. Updating a secret must not silently apply an unchecked/default `secret:false`. Show the server's required-name and type errors without echoing entered values.
- Show safe declaration requirements or, at minimum, names-only validation errors that identify missing production values. Never prepopulate production forms from local dotenv files.
- Keep ZIP-only deployment and the existing ownership policy. Do not add archive formats, project linking, or secret reveal/export.

## Files to change

| Area | Existing integration points / planned files |
| --- | --- |
| CLI | Register commands and deploy options in `cmd/hostedat/main.go`. Add `secret.go`, `env.go`, and command behavior tests. Use Cobra input/output streams. Add a terminal helper dependency only for hidden input. |
| Shared validation | Add JSON/YAML parsing, the shared declaration model, safe diagnostics, dotenv handling, and behavioral tests in `internal/envschema/`. Keep database access out of the parser. |
| Client | Update `internal/client/client.go`, `sites.go`, `deploy.go`, and `deploy_test.go`. Add typed methods in `env.go` and HTTP behavior tests. |
| API | Update `internal/api/worker_handlers.go`, `routes.go`, `deploy_handlers.go`, and `site_serving.go`. Add the environment service and DTO, with coverage for single writes, bulk writes, validation, deploy, and rollback. |
| Persistence/security | Update `internal/models/models.go`, `database.go`, `internal/config/config.go`, `cmd/server/main.go`, and `config.example.yaml`. Add encryption and operator-maintenance tools. Never put a real key in examples. |
| Runtime | Update `internal/workeradapter/env.go`, `cron.go`, the API fetch builder, server cron factory, dispatcher factory signature, and existing environment tests. Do not expand queue/service features that are not wired into production. |
| Artifact policy | Update `internal/storage/storage.go`, the shared resolved-file policy, and `internal/workeradapter/assets.go`. Cover extraction, static serving, assets, and CLI ZIP packaging. |
| Dashboard | `web/src/lib/api/types.ts`, `client.ts`; `web/src/lib/components/sites/worker/EnvVars.svelte`, `WorkerPanel.svelte`. |
| Documentation | Existing `docs/public/{api-reference,cli-reference,worker-runtime,architecture}.md`, `docs/src/pages/docs/{cli,workers,getting-started}.astro`, server config documentation, and `CHANGELOG.md`. Leave README unchanged. |

## Sequential implementation tasks

Work through these tasks in order. Each task includes its focused verification. The acceptance matrix below remains the completion checklist for the whole change. These are planned tasks, not completed work.

1. [x] Parse and validate flat declarations in `internal/envschema`. Support equivalent `env.json` and `env.yml` mappings, types, enums, required/optional rules, strict decoding, size limits, and value-free diagnostics. Verified equivalent inputs, malformed documents, duplicate keys, invalid values, and absent versus empty values with race-enabled focused tests and a disposable consumer harness. No version or server/client sections added. Evidence: `native-secrets-task01-verification.md`.

2. [x] Add local schema discovery and dotenv validation helpers. Resolve defaults only in the invocation directory, require `--schema` when both files exist, and fail on explicitly missing files. Parse bounded dotenv input without shell execution and overlay explicitly set shell variables for local checks only. Verified quoted `#`, CRLF, multiline values, whitespace, empty overrides, discovery conflicts, literal substitutions, and symlink/hard-link path aliases with focused race-enabled tests and a disposable real consumer in a read-only, network-isolated sandbox. Evidence: `native-secrets-task02-verification.md`.

3. [x] Remove secret disclosure from management responses. Explicit public DTOs omit secret values and server-side placeholders on writes/lists, preserve empty plaintext values, and environment routes set `Cache-Control: no-store` before auth/rate limits. Dashboard discriminated types and local placeholders preserve classification on updates. Verified focused race-enabled handler requests, redacted errors, authorization/site scoping, and actual headless dashboard updates against a disposable real server/QuickJS worker. TypeScript check and dashboard build pass; the whole-app Svelte check reports seven unrelated existing errors. Evidence: `native-secrets-task03-verification.md`.

4. [x] Add encryption and operator key configuration. Versioned AES-256-GCM storage binds site/name/version, uses random nonces and key IDs, and requires an operator-owned 0600/0400 JSON key file. Focused tests prove tampering, wrong/missing keys, cross-binding reuse, safe errors and malformed/duplicate/unrestricted key-file refusal. Evidence: `native-secrets-task05-verification.md`.

5. [x] Implement offline migration and key rotation. Server offline commands transactionally add encrypted fields, encrypt legacy secrets, clear live plaintext, verify counts/authentication and install uniqueness only after explicit duplicate resolution. Strict startup validation precedes traffic/workers. Documented separate backups, WAL/free-page/history exposure, compaction/retirement, rotation/recovery and coordinated rollback. Actual disposable legacy migration, forced rotation rollback, successful rotation, duplicate/key refusal and restart were exercised. Evidence: `native-secrets-task05-verification.md`.

6. [x] Add deployment declaration persistence and same-site database coordination. Nullable internal declaration snapshots and default-off enforcement flags preserve absent versus explicit empty schemas. Shared write-before-read SQLite transactions coordinate environment mutations, activation/rollback and binding creation across connections/processes. Duplicate startup/lookup refusal and an explicit offline uniqueness installer avoid implicit winners. Focused race-enabled tests plus two real server processes prove one-row concurrent upserts after uniqueness, lock serialization and failed-activation rollback; real QuickJS invocation succeeds before/after rollback. Evidence: `native-secrets-task04-verification.md`.

7. [x] Move single writes and deletes into the shared environment service. Enforce name rules, explicit field presence, encrypted writes, active-declaration constraints, ownership/site scoping, and metadata-only reads. Preserve plaintext bindings outside declared names and prevent implicit secret downgrades. Focused race-enabled tests and actual production-server HTTP/QuickJS smoke verify required-secret deletion/downgrade rejection, active type constraints, safe post-commit audits and best-effort audit failure. Evidence: `native-secrets-task06-verification.md` (encrypted storage/runtime cutover also in task 05).

8. [x] Add atomic bulk writes and read-only remote validation routes. Complete bounded batches commit through the coordinated service transaction; candidates constrain supplied declared secrets without activation and omitted bindings/IDs remain intact. Focused race-enabled tests and actual production-server HTTP/QuickJS smoke verify invalid/storage-failure rollback, batch/value/body bounds, duplicates/omissions, decrypted classification/type checks and cross-user/wrong-site isolation. Evidence: `native-secrets-task06-verification.md`.

9. [x] Enforce binding collisions in both directions. Static runtime reservations and exact site-bound name checks cover single/bulk writes, all candidate/active declaration names, KV, D1, storage and Durable Object provisioning under shared database coordination. Preserve other legacy namespace syntax; fail closed on existing environment/binding collisions. Focused race-enabled checks, two real production server processes, real QuickJS fetch/KV/D1/ASSETS and real SeaweedFS prove both orders, 24 one-winner concurrent creates and compensated storage cleanup. Evidence: `native-secrets-task07-verification.md`.

10. [x] Propagate environment-loading failures through every runtime consumer. `BuildEnvFromDB`, HTTP construction and cron/dispatcher factories return errors and decrypt only into new in-memory snapshots. Actual QuickJS HTTP returns generic configuration failures without execution after tampering; cron regression proves skipped failures do not mark a run; dispatcher fails closed. A real in-flight request retains its snapshot while the next request sees an updated secret. Evidence: `native-secrets-task05-verification.md`.

11. [ ] Secure archive extraction and static serving. Preflight original and flattened paths, reject dotenv components case-insensitively, reject duplicate normalized destinations, and retain traversal/symlink/type/size checks and partial-extraction cleanup. Accept only canonical artifact-root `_hostedat.env.json`, validate and remove it from assets, and block resolved access through static routes and `env.ASSETS.fetch`. Verify wrapped archives, normalized aliases, rewrites, SPA/custom-domain paths, and direct API uploads.

12. [ ] Enforce declarations during deploy, rollback, and invocation. Validate stored secret presence, classification, and types inside the activation transaction before switching `ActiveDeployID`. Set the site's enforcement flag on its first successful declaration-bearing deployment. Require later manifests, validate historical declarations against current secrets on rollback, and validate bindings when building invocation environments. Verify `required: true` blocks missing secrets, failed activation leaves the previous deployment active and removes staged artifacts, schema-less legacy sites still work, and same-site mutation races cannot commit invalid active bindings.

13. [ ] Finish dashboard provisioning before first deployment. Show the environment editor before `hasWorker`, preserve secret classification on updates, and display safe requirement errors without echoing entered values. Verify the actual dashboard can provision secrets on an empty owned site and then deploy its first declaration-bearing worker. Keep ZIP-only uploads and existing ownership behavior.

14. [ ] Add capability negotiation and typed Go client methods. Advertise the versioned secret/environment capability only once the server enforces the encrypted storage and declaration rules. Add environment methods using existing auth/error/HTTP helpers. Require HTTPS except explicit loopback development and refuse cross-origin write redirects. Verify old-server rejection, value-free errors, and no sequential fallback for bulk sync.

15. [ ] Add native `secret put`, `sync`, `list`, and `delete` commands. Reuse site resolution and Cobra streams. Support hidden terminal input and explicit `--stdin`, bounded values, named sync files, local `.env` rejection, metadata-only output, and existing confirmation behavior with `--yes`. Validate declarations before writes. Verify real PTY input, piped CI input, target selection, redaction, atomic sync, and preservation of unrelated target secrets.

16. [ ] Add local and remote `env check` commands. Connect local checks to the schema/dotenv helpers and remote checks to server-side validation. Verify local checks make no network requests and remote requests contain declarations only, never local values. Every declared variable is a server-side secret. Do not read build-shell values for client configuration, inject browser values, or run an application build.

17. [ ] Integrate declarations with CLI upload packaging. Serialize either source format as canonical flat JSON at `_hostedat.env.json`, exclude the selected source declaration from ordinary packaging, and reject conflicting manifests. Require server capability support and run read-only preflight before upload. Verify deployment from `dist`, required-secret failures and success, unchanged legacy schema-less uploads, and no secret values in archives. Deploy must not sync values or build the application.

18. [ ] Run the complete acceptance matrix and document verified behavior. Launch a disposable server/database/storage directory, build the real CLI, exercise secret operations and failed/successful deploys, and fetch the deployed worker. Check public assets, logs, and management responses for secret markers. Run the relevant Go packages and the existing full suite once, plus dashboard typecheck/build for changed API consumers. Record unavailable QuickJS/V8 coverage. Update the existing CLI/API/runtime/architecture and server configuration docs and `CHANGELOG.md`. Leave README unchanged. Do not describe unexercised commands or security guarantees as available.

## Acceptance matrix

| Area | Required observable result |
| --- | --- |
| Native CLI | The built CLI lists secret/env groups. The hidden prompt emits no credential. Piped CI input works without a TTY. Missing site, auth, or input errors do not leak values. |
| Local checks | Quoted `#`, CRLF, multiline input, literal whitespace, absent vs empty, shell precedence, invalid types, optional keys, and unknown declarations behave as documented. Diagnostics never include supplied values. |
| Put/update | A new secret appears remotely by name. Replacing it preserves unrelated names and IDs. Management responses never disclose it. The worker receives the new value on its next invocation. |
| Sync atomicity | An invalid entry or storage failure leaves all prior values unchanged. With a schema, success writes only supplied, declared variables as server-side secrets to the selected target. Omitted names and unrelated bindings remain unchanged. No implicit deletion or pruning. |
| Isolation | Other users and wrong-site IDs cannot list, write, delete, or validate. Owner/admin authorization matches existing routes. |
| Storage | A secret marker does not appear in database secret fields, logs, or API responses. Ciphertext decrypts only for the intended binding. Wrong keys and tampering fail closed. Migration removes plaintext from live rows and supports documented recovery. |
| Declaration rules | Invalid/reserved/colliding names, duplicate keys, unknown rule fields, grouped or versioned documents, and malformed input fail before writes. Prefixes do not classify values. Every declared variable is stored as a server-side secret. |
| Declaration formats | Equivalent `env.json` and `env.yml` files produce identical validation results and canonical JSON metadata. Discovery works with either file alone, rejects both unless `--schema` selects one, and honors explicit paths. Both formats reject duplicate keys and unknown fields. YAML rejects multiple documents, non-string mapping keys, and incorrect field types without disclosing input values. |
| No application build | Secret sync writes only to the selected target environment. Check and deploy never build an application, inject browser values, or read build-shell values for client configuration. |
| Remote check | No local `.env` values enter a request. The server validates remote values. Plaintext bindings cannot satisfy required secret declarations. |
| Deploy | Missing or invalid secrets prevent activation through CLI, dashboard, and direct API. Correct secrets allow deployment. `.env`, `.env.*`, symlinks, disguised archive paths, and duplicate entries cannot become public assets. |
| Runtime/rollback | Production HTTP and cron receive decrypted string bindings and reject invalid required environments. Retained dispatcher interfaces propagate loader failures. Rollback checks the selected artifact against current secrets and never restores old values. |
| Concurrency | A same-site deploy racing a secret delete or update cannot activate a deployment with invalid bindings. Concurrent puts of one name produce one row. |
| Regression | Existing plain env management, namespace bindings, schema-less sites, deployment failure behavior, and CLI auth/site resolution retain their established contracts. |
| First deployment | The dashboard allows required secrets to be configured before a worker exists. The first declaration-bearing deployment then succeeds. |
| Mixed versions | The new CLI refuses servers without the declared capability. The server enforces opted-in requirements for uploads from older CLI/dashboard versions and the raw API. |

Use the existing httptest and temporary-database patterns for permanent tests. Assert behavior and security guarantees a consumer can observe, not copies of implementation details. Update tests that expect secret disclosure. Test hidden terminal input through a PTY, not only a mocked reader.

For the final smoke run, launch a disposable server with a temporary database and storage directory. Build and run the actual CLI. Exercise put, sync, list, delete, and failed and successful deploys. Fetch the deployed worker. Check that the secret marker is absent from public assets, logs, and management responses. Never use real credentials or production sites.

After implementation, run the relevant Go packages and the existing full-suite command once. Run dashboard typecheck/build when API consumers change. Use the repository's documented QuickJS/V8 prerequisites. A mocked worker does not prove either engine works. Record any engine coverage that could not run.

This plan does not run the suite or claim that any acceptance check has passed.
