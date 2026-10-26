# Changelog

## Unreleased

### Changed

- Add deploy declaration discovery and `--schema`: JSON/YAML from the invocation
  directory becomes canonical private ZIP metadata even when uploading `dist`.
  Exclude the selected source and reject separately supplied canonical conflicts.
  Manifest uploads require capability negotiation and declaration-only read-only
  preflight; activation remains the server's transactional decision. Preserve
  legacy schema-less uploads and case-insensitive dotenv/symlink exclusions;
  never load local values, sync secrets or build applications. Focused race-enabled
  regressions, five inspected built-CLI manifest uploads to the production server,
  old-server/missing-secret refusal and actual QuickJS fetches were exercised.

- Add `env check [--schema <path>]` for offline root-dotenv plus explicit shell
  validation, and `env check --remote --site <site>` for read-only stored-secret
  checks. Local mode needs no credentials/network and never launches apps or
  writes files; remote mode sends canonical declarations only, never local
  dotenv/shell values, and rejects plaintext classification. Focused race-enabled
  command regressions, built-CLI checks with socket syscalls denied, captured
  production-server validation requests and real QuickJS fetches were exercised.

- Add native `secret put/sync/list/delete` commands using existing credential/site
  resolution, hidden bounded terminal input and explicit `--stdin` for CI.
  Put/sync support discovered JSON/YAML declarations and `--schema`; atomic sync
  rejects local `.env` aliases, preserves omitted names/IDs and never builds apps.
  List/JSON emit secret metadata only; delete refuses plaintext and honors
  confirmation/`--yes` and active requirements. All commands negotiate capability,
  require HTTPS except explicit loopback development and refuse redirects.
  Focused race-enabled regressions, a built CLI against the production server,
  real hidden-input PTY capture, SQLite-trigger rollback and real QuickJS
  next-invocation replacement were exercised.

- Make the dashboard environment editor available on owned empty sites, before
  the first worker deployment. Keep worker-only tabs hidden until a worker exists,
  preserve secret classification on replacement, and display value-free required
  name/type errors. Dashboard uploads now offer only supported ZIP archives and
  clear a previously selected ZIP when an unsupported file is chosen. Actual
  headless first-deployment provisioning, encrypted secret CRUD, ownership denial
  and real QuickJS fetches were exercised.

- Add authenticated per-site atomic environment bulk merge and read-only remote
  declaration validation. Entire bounded batches commit or roll back together,
  preserve omitted bindings/IDs, and return metadata only. Candidate declarations
  constrain supplied secrets without activation; single/bulk writes and deletion
  enforce established running snapshots. Remote checks authenticate/decrypt stored
  secrets and reject plain classification. Invalid/storage rollback, isolation,
  value-free responses/audits/logs and actual QuickJS updates were exercised.
  Deployment activation now establishes the running snapshot as described below.

- Environment management write/list responses now use explicit public DTOs:
  secret values and server-side mask placeholders are omitted, while empty
  plaintext values remain present. Environment handlers disable response
  caching, including handled errors. The dashboard uses discriminated
  secret/plain types, renders placeholders locally, and preserves classification
  when updating a name. Verified actual handler requests and a headless
  dashboard update against a disposable server with a real QuickJS worker.
  Persisted secrets now use authenticated encryption as described below.

### Security

- Advertise the versioned `secrets-env-v1` capability alongside unchanged version
  fields. Add typed Go environment methods and negotiated manifest ZIP uploads;
  secret mutations fail with upgrade guidance on unsupported servers and bulk
  never falls back to sequential writes. Require HTTPS with explicit loopback
  development opt-in; refuse redirects in the shared HTTP client and keep secure
  errors body-free. Focused race-enabled client/handler checks and actual
  production-server/QuickJS smoke verified atomic rollback, metadata redaction
  and next-invocation secret replacement. Native secret commands, env checks and
  schema-aware CLI packaging are integrated above.

- Enforce canonical declarations before compilation and again under the shared
  database-backed activation lock, validating required presence, decrypted types,
  secret classification and binding collisions. The first manifest activation
  makes later manifests mandatory; explicit empty schemas can remove requirements
  without re-enabling schema-less deploys. Rollback validates current secrets,
  never restores old values, and rejects schema-less targets on enforced sites.
  Failed activation preserves the running deployment and removes staged assets,
  bytecode and compiled pools. HTTP/cron validate the exact invocation snapshot
  with fresh string bindings and fail closed. Focused race regressions, separate-
  connection mutation interleavings, actual QuickJS fetch/cron, production raw
  HTTP and headless dashboard uploads were exercised.

- Preflight original and wrapper-flattened ZIP paths before writes; reject
  dotenv components case-insensitively, traversal/absolute/drive/UNC/NUL/backslash
  paths, duplicate normalized destinations, file/directory conflicts and unsafe
  types, retaining size bounds and partial-extraction cleanup. Parse canonical
  artifact-root `_hostedat.env.json` separately into internal deployment metadata,
  never public assets. Static serving and worker ASSETS block reserved metadata
  and historic dotenv files through normalized paths, rewrites, SPA and physical
  symlink aliases. CLI exclusions now cover mixed-case dotenv paths and retain
  all symlink exclusions; ordinary packaging refuses reserved metadata.
  Focused race-enabled regressions, hostile raw HTTP uploads, real QuickJS ASSETS
  and built-CLI smoke passed. This is filename policy, not bundle-content scanning;
  schema-aware CLI packaging remains a subsequent cutover.

- Enforce environment/binding collisions in both directions for KV, D1, object
  storage and Durable Object provisioning under the shared database-backed site
  transaction. Check all declaration names, including absent optional secrets;
  reserve ASSETS and prototype-sensitive names case-insensitively. Preserve
  other legacy namespace syntax and exact per-site name comparisons. Existing
  environment/runtime-binding collisions fail closed rather than shadowing.
  Focused race-enabled checks and two real server processes prove one winner
  for conflicting creates; actual QuickJS and SeaweedFS smoke verifies intact
  string/object bindings and storage loser cleanup.

- Encrypt worker secrets with versioned AES-256-GCM, random nonces, operator key
  IDs and site/name/version associated data. Require a restricted key file and
  explicit offline migration (including uniqueness installation) before startup.
  Offline rotation verifies and commits atomically; neither operation logs values.
  Parameterized SQL logging hides bound values. Plain bindings remain supported.
- Route environment management and runtime loading through one shared service.
  Missing values are rejected; omitted classification preserves existing secrets.
  HTTP fails closed before execution on storage/decryption failure; cron skips
  invalid environments without reporting a run, and dispatchers propagate errors.
  Disposable legacy migration, rotation/rollback, restart and actual QuickJS
  requests prove decrypted strings, next-invocation updates and in-flight snapshots.
  Native secret CLI integration remains pending.

### Internal

- Persist nullable internal deployment declaration snapshots and a default-off
  site enforcement flag, preserving absent versus explicit empty declarations.
  Coordinate environment writes/deletes, deployment/rollback activation and
  binding creation through a shared SQLite-backed transaction protocol across
  connections and processes. Correct the driver's WAL/busy-timeout options.
  Startup and name lookup refuse duplicate environment names; an explicit
  offline-maintenance helper installs uniqueness only after resolution.
  Focused race-enabled tests and two real server processes verify concurrent
  same-name upserts, lock serialization and activation rollback, with actual
  QuickJS invocation before/after rollback. Deployment activation and mutations
  now enforce stored running declarations.
- Add the shared `internal/envschema` JSON/YAML secret-declaration parser,
  deterministic canonical JSON, strict rule/name checks, and value-free
  validation diagnostics. Focused race-enabled tests and a disposable consumer
  harness exercise equivalent formats, invalid/duplicate/oversized documents,
  required/optional/empty values, exact whitespace, and redaction. This package
  does not yet enable secret commands, encrypted storage, or deployment checks.
- Add invocation-scoped schema discovery and bounded, read-only dotenv helpers
  in `internal/envschema`, using maintained `godotenv` parsing with interpolation
  disabled and duplicate rejection before overwrite. Local shell overlays
  preserve explicit empty values; named sync inputs reject the project's local
  `.env`, including symlink and hard-link aliases. Focused race-enabled tests
  and a disposable consumer in a read-only, network-isolated sandbox exercise
  discovery conflicts, CRLF/multiline/quote/comment/whitespace behavior, literal
  substitutions, redacted errors, size limits, and path identity. These helpers
  are not yet CLI commands or a remote secret-sync implementation.


## v2.0.0 — V8 Worker Runtime Migration

**Released:** 2026-02-20
**Branch:** `migrate/v8go` (16 commits, 160 files changed, +54,971 / -2,563 lines)

### Breaking Changes

- **Runtime engine replaced:** QuickJS/Wazero WASM interpreter removed, replaced with native V8 via [`tommie/v8go`](https://github.com/tommie/v8go). Workers now execute on the V8 engine with significantly better performance and broader Web API compatibility.
- **Configuration:** `worker.wasm_path` replaced with `worker.data_dir` in `config.yaml`.
- **Go 1.25+ required:** The minimum Go version is now 1.25 (up from 1.22).
- **CGO required:** V8 bindings require CGO enabled at build time (Linux only; no Windows native build).

### New Features

#### Web APIs
- **Cache API** — `caches.default` and `caches.open(name)` with `match`, `put`, `delete` backed by SQLite storage with TTL expiry
- **D1 Database** — SQL database bindings with `prepare`, `bind`, `first`, `all`, `run`, `batch`, `exec`; site-prefixed file isolation and path traversal protection
- **Durable Objects** — Stateful coordination primitives with `DurableObjectNamespace.get(id)`, `DurableObjectStub.fetch()`, transactional storage, and alarm scheduling
- **HTMLRewriter** — Streaming HTML transformation engine with `element()`, `text()`, `comments()` handlers and full selector support
- **WebSockets** — Full `WebSocket` client with `open`, `message`, `close`, `error` events and binary frame support
- **TCP Sockets** — `connect(address, options)` for raw TCP/TLS connections with `readable`/`writable` stream pairs
- **EventSource** — Server-Sent Events client with auto-reconnect, `lastEventId` tracking, and custom headers
- **URLPattern** — URL pattern matching compatible with the URLPattern Web API spec
- **Queues** — `Queue.send()` and `Queue.sendBatch()` for asynchronous message passing between workers
- **Service Bindings** — Inter-worker RPC via `serviceBinding.fetch()` with request forwarding
- **MessageChannel / MessagePort** — Structured clone messaging between execution contexts
- **BYOB Readers** — `ReadableStreamBYOBReader` for zero-copy reading into caller-supplied buffers

#### Crypto
- **ECDH / X25519** — Key agreement via `deriveBits` and `deriveKey` for P-256, P-384, P-521, and X25519 curves
- **Ed25519** — EdDSA signing and verification
- **RSA** — RSA-OAEP encryption/decryption, RSA-PSS and RSASSA-PKCS1-v1_5 signing
- **AES Key Wrapping** — AES-KW and AES-GCM key wrap/unwrap
- **HKDF / PBKDF2** — Key derivation functions
- **DigestStream** — Streaming hash computation (`SHA-1`, `SHA-256`, `SHA-384`, `SHA-512`)

#### Compression
- **Brotli** — `CompressionStream('br')` and `DecompressionStream('br')`
- **Gzip / Deflate** — `CompressionStream('gzip'|'deflate')` and `DecompressionStream('gzip'|'deflate'|'deflate-raw')`

#### Streams
- **TextEncoder/DecoderStream** — Streaming UTF-8 encode/decode via TransformStream
- **ReadableStream** — Full implementation with `tee()`, `pipeTo()`, `pipeThrough()`, `cancel()`
- **WritableStream** — With `abort()` and backpressure support
- **FixedLengthStream** — Validates content length and signals early on mismatch

#### Worker Engine
- **Worker Pool** — Configurable pool with per-site isolate reuse, memory limits (`v8.WithResourceConstraints`), and automatic invalidation on timeout
- **Event Loop** — Microtask-aware event loop with `setTimeout`/`setInterval`, promise settling, and `waitUntil` lifecycle
- **ES Module Wrapping** — `export default { fetch }` syntax support via `wrapESModule()`
- **Unhandled Rejection Tracking** — `unhandledrejection` event for debugging promise errors
- **Bundle Support** — Multi-file worker bundles with automatic dependency resolution

### Improvements

- **Fetch API** — Private network protection (CIDR ranges parsed at init), redirect following, streaming bodies
- **KV Store** — List with prefix/cursor pagination, expiration TTL, metadata support
- **R2 Storage** — `head`, `list`, `delete`, `createMultipartUpload`, `uploadPart`, `completeMultipartUpload`
- **Console** — Structured logging with `console.log/warn/error/debug/info` piped to `WorkerLog` model
- **Cron Scheduling** — Cron-triggered `scheduled` event handlers with jitter and distributed locking
- **FormData** — Multipart form parsing with `File` and `Blob` support
- **Abort Signals** — `AbortController`/`AbortSignal` with timeout support

### Database

- **New models:** `D1Database`, `DurableObjectNamespace`, `S3Credential`, `RevokedToken`, `CacheEntry`
- **Auto-migration:** All new models included in `AutoMigrate` call
- **D1 isolation:** Database files prefixed with `{siteID}_` to prevent cross-site access
- **Durable Object isolation:** Namespace IDs prefixed with `{siteID}:` for tenant separation
- **Storage binding uniqueness:** Startup check prevents duplicate binding names per site

### Testing

- **Comprehensive coverage:** 80+ new test files across all packages
- **API test suite:** Full handler coverage for admin, auth, deploy, site, storage, worker endpoints
- **Worker runtime tests:** Every new API (Cache, D1, Durable Objects, WebSockets, TCP, HTMLRewriter, etc.) has dedicated test files
- **Cross-site isolation tests:** Verify D1 and Durable Object tenant boundaries
- **Path traversal tests:** Validate `ValidateDatabaseID()` rejects malicious inputs
- **Crypto regression tests:** Cover BigInt trap, key import/export round-trips, algorithm edge cases

### Documentation

- **worker-runtime.md** — 16 new API sections with usage examples and Cloudflare compatibility table
- **architecture.md** — Updated execution flow and new model references
- **workers.astro** — New interactive examples, environment binding subsections
- **README.md** — Updated feature highlights and Web API listing

### Dependencies

- **Added:** `github.com/tommie/v8go` (V8 JavaScript engine bindings)
- **Removed:** QuickJS, Wazero (WASM runtime)
- **Updated:** `go.mod` now requires Go 1.25+

### Migration Guide

1. Update `config.yaml`: replace `worker.wasm_path` with `worker.data_dir`
2. Ensure build environment has CGO enabled (Linux required for V8)
3. Run the binary — `AutoMigrate` will create new tables automatically
4. Existing KV namespaces, environment variables, and cron schedules are preserved
5. Worker scripts using `export default { fetch(request, env) {} }` will work as before
