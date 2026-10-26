# Native secrets and environment: sequential implementation tasks

Status: reviewed task breakdown; tasks 01–11 implemented and verified. Tasks 12–15 have not started. Execute in numbered order. The detailed contract and acceptance matrix in `native-secrets-env-plan.md` remain authoritative; this file changes sequencing, not scope.

## Review decisions

- Current implementation is still the baseline: CLI has no secret/env groups; API writes return secret values; storage uses plaintext Value; runtime loader cannot return errors; dashboard requires an existing worker.
- Keep response redaction and dashboard DTO adaptation together, before encryption.
- Treat encrypted persistence, offline maintenance, startup checks, and runtime consumers as one cutover. Do not merge a server that encrypts writes but cannot decrypt invocation bindings, or that requires migration without providing maintenance tooling.
- Establish database-backed coordination before attaching active-schema checks to mutations or activation. Test separate connections, not only goroutines sharing a mutex. Select the mechanism against the actual configured database implementation.
- Secure artifact paths before accepting canonical manifests. Add deployment, rollback, invocation, and binding-creation enforcement before advertising capability support.
- Preserve the version endpoint's existing version/min_cli_version fields when adding capabilities; migrate typed clients if its response shape changes.
- Each task must leave its changed contracts and callers coherent. Intermediate work is not a release of the completed secrets workflow. Do not enable the capability until the complete server contract passes.
- No application builds, browser injection, project linking, automatic dotenv sync, reveal/export, plaintext fallback, or sequential bulk fallback. Leave README unchanged.

## Execution rules

For each task: inspect current code and existing test patterns; inspect references before exported-interface changes; implement; exercise the changed path; retain only consumer-visible regression tests; update existing relevant documentation/changelog after proof. Do not claim checks not run. Use disposable credentials/data only. No production migration as part of development.

### 01 — Shared declarations and safe validation

- [x] Implement `internal/envschema` flat JSON/YAML declarations and canonical JSON serialization.
- Reject duplicate names/fields, unknown fields, grouped/versioned documents, non-string YAML keys, multiple documents, coercion, unsupported types, and oversized input.
- Implement name grammar, required-by-default semantics, absence versus empty, string/finite-number/boolean/absolute-URL/email/JSON/string-enum checks; document exact boolean and whitespace behavior. All declarations denote secrets, including VITE_ names.
- Keep diagnostics value-free and database-free. Separate static name validation from database-aware binding collision checks.
- Proof: equivalent JSON/YAML inputs, malformed/duplicate/oversized documents, optional invalid values, empty required values, and marker-redaction cases. Exercise parsing/validation through a disposable harness in addition to focused tests.

### 02 — Schema discovery and bounded dotenv inputs

- [x] Add invocation-directory discovery, explicit `--schema` resolution, format selection, and root-dotenv association.
- Both defaults present requires explicit selection; explicit missing/unsupported files fail. No parent search. Resolve once per command.
- Use a maintained dotenv parser, with duplicate detection before parser overwrite; bounded input; no shell execution; safe errors. Document quotes, comments, CRLF, multiline and whitespace semantics.
- Provide local-only shell overlay preserving explicitly empty values, and sync-source rejection for the project's local `.env` (including equivalent resolved paths).
- Proof: quoted #, CRLF, multiline, literal substitutions, duplicates, whitespace, empty overrides, discovery conflicts, symlink/path aliases, and no writes/network during local helper execution.

### 03 — Redact management API and adapt dashboard DTOs

- [x] Replace model serialization with explicit public DTOs for single-write/list responses; omit secret value, retain empty plaintext value, set Cache-Control: no-store.
- Update `web/src/lib/api/{types.ts,client.ts}` and `EnvVars.svelte` together: discriminated plain/secret types, local placeholders only, no placeholder resubmission, preserve classification on updates.
- Preserve owner/admin and site/ID scoping. Remove secret-disclosure expectations from existing tests rather than re-pinning implementation wording.
- Proof: actual handler requests containing a disposable marker never return it, including errors; empty plaintext round-trips; actual dashboard can update without revealing/replacing an existing secret. Run changed-consumer type checks.
- Evidence: `.scratch/native-secrets-task03-verification.md`; real handler requests, disposable server/QuickJS worker and headless dashboard update passed. TypeScript check/build passed; whole-app Svelte check reports seven unrelated existing errors, listed in the evidence.

### 04 — Persistence snapshots and database coordination

- [x] Add nullable canonical declaration snapshot to Deployment and enforcement flag to Site; absent and explicit empty declaration remain distinct.
- Establish one database-backed same-site transaction protocol for environment mutation, deploy activation, rollback, and collision-sensitive binding creation. Handle failures without partial writes.
- Detect duplicate (site_id,name) rows and refuse implicit winner selection; define operator resolution and uniqueness installation for the maintenance task.
- Proof: separate database connections serialize conflicting operations; concurrent same-name upserts ultimately have one row once uniqueness is installed; failed transactions leave prior state intact. No process-local-only lock.
- Evidence: `.scratch/native-secrets-task04-verification.md`; focused race-enabled tests, separate SQLite connections and two real server processes prove serialization, stable one-row concurrent upserts with uniqueness, failed-activation rollback and real QuickJS invocation before/after rollback.

### 05 — Atomic encrypted-storage and runtime cutover

- [x] Implement AES-256-GCM versioned ciphertext, random nonce, key ID, and site/name/version associated data; secrets never persist in Value, plaintext bindings remain supported.
- Add restricted operator-provisioned key-file configuration, strict startup key/data validation, and parameterized safe SQL logging. Never derive/generate replacement keys implicitly.
- Provide offline transactional migration and rotation commands: reject duplicate rows until explicitly resolved, install uniqueness, encrypt legacy rows and clear live plaintext, verify counts/decryption, and fail atomically on errors. Require stopped writers/workers.
- Move all environment reads/writes through a small shared service. Single writes distinguish omitted value/secret from explicit values and cannot implicitly downgrade. Deletes/list remain metadata-only without decrypting values.
- Change BuildEnvFromDB to return (*worker.Env,error); migrate BuildEnvOptions, HTTP builder/dependencies, cron factory/invocation, and retained dispatcher interfaces/tests. Decrypt in memory only; HTTP must not execute on failure, cron must not report success on skipped invocation. No plaintext fallback.
- Update startup wiring and maintenance documentation together: separate key/database backups, WAL/free-page/old-backup exposure, compaction/retirement, rotation, restore and coordinated binary rollback.
- Proof: run maintenance on a disposable legacy database; inspect live secret fields for marker absence; restart server; invoke a worker with decrypted string bindings; update a secret and observe next invocation versus existing snapshot. Exercise tampering, wrong/missing keys, cross-binding ciphertext reuse, duplicates, and maintenance rollback. Record real engine coverage.
- Evidence: `.scratch/native-secrets-task05-verification.md`; focused encrypted-storage/consumer tests, actual offline migration/rotation (including forced rollback), key/duplicate refusal, and restarted production-server QuickJS fetches prove encrypted live rows, decrypted strings, in-flight snapshots, next-invocation updates and fail-closed tampering. Declaration enforcement remains tasks 06/09.

### 06 — Atomic bulk writes and remote validation

- [x] Add bounded bulk and declaration-validation routes under existing authenticated per-site worker/env routes, using the shared service and transaction protocol.
- Validate entire batches, reject duplicate/empty/oversized batches and omitted values; candidate schemas validate but never activate. Schema-backed supplied entries must be declared secrets. Merge only: preserve omitted/unrelated names.
- Enforce active declaration constraints for single/bulk writes and deletion; forbid required-name deletion/downgrade and updates invalidating the running deployment. Preserve undeclared legacy plaintext bindings.
- Add metadata-only no-store responses and post-commit best-effort audits containing names/counts/actions only. Safe API errors cannot contain parser/request/SQL values.
- Proof: actual HTTP bulk success and invalid/storage-failure rollback, local candidate versus active-schema constraints, remote validation of decrypted values/classification, omitted fields, and cross-user/wrong-site denial for every operation.
- Evidence: `.scratch/native-secrets-task06-verification.md`; focused race-enabled tests and actual production-server HTTP/QuickJS smoke prove atomic merge and invalid/storage rollback, candidate-versus-active constraints, decrypted remote validation, metadata-only results/audits, best-effort auditing and all-operation isolation. Deployment declaration activation remains task 09.

### 07 — Binding collisions in both directions

- [x] Enforce runtime-reserved names and site-bound namespace collisions for environment writes, declarations, and creation of ASSETS/KV/D1/storage/durable-object/other supported bindings.
- Use the same database coordination as environment changes; inspect every actual binding creation path, not only worker env handlers.
- Proof: both creation orders and concurrent conflicting creates fail without shadowing; unrelated legacy namespace behavior remains intact.
- Evidence: `.scratch/native-secrets-task07-verification.md`; focused race-enabled checks and actual two-process production HTTP/QuickJS/SeaweedFS smoke prove both creation orders, one committed winner in 24 concurrent conflicts, absent optional declaration reservations, intact legacy naming and real S3 loser-bucket cleanup.

### 08 — Artifact path and public-serving policy

- [x] Share case-insensitive dotenv component and reserved-metadata policy between CLI/server/resolved serving.
- Preflight every original ZIP path and normalized destination after supported wrapper flattening before writes. Reject dotenv wrappers/files, traversal, NUL, absolute/drive/UNC/backslash paths, duplicate normalized destinations, symlinks, unsupported types, and size violations; retain cleanup.
- Accept only artifact-root `_hostedat.env.json` after flattening; parse canonical declarations and remove metadata from public assets. Reject misplaced/conflicting metadata rather than treating it as content.
- Block reserved metadata and historic dotenv assets through resolved static routes, rewrites, normalized aliases, SPA/custom-domain paths, and env.ASSETS.fetch. Policy is filename-based, not detection of secrets embedded in bundles.
- Proof: real raw-API ZIP uploads with wrappers, duplicate/path attacks and failure cleanup; actual static and worker-ASSETS requests cannot fetch blocked content. Legacy safe archives still deploy.
- Evidence: `.scratch/native-secrets-task08-verification.md`; focused race-enabled archive/client/static/adapter regressions, actual production HTTP rejection of 28 hostile/oversized raw uploads with failure cleanup, real QuickJS ASSETS and built-CLI smoke prove private canonical metadata, blocked historic artifacts/aliases/rewrites, preserved SPA behavior and safe legacy uploads.

### 09 — Deployment, rollback, and invocation enforcement

- [x] Persist canonical snapshots; validate secret presence, classification, decrypted types, and collisions before activation and again inside coordinated activation transaction.
- First manifest activation sets enforcement flag; later schema-less deploys fail. Explicit empty schemas may remove requirements. Legacy unenforced schema-less sites remain supported.
- Validate rollback target against current values without restoring old values; reject schema-less rollback on enforced sites. Validate loaded invocation environment against the active snapshot, parsed once per loaded snapshot rather than disk per request.
- Ensure failed activation leaves previous deployment active and cleans staged assets/compiled artifacts.
- Proof: actual missing/invalid-secret failed and successful deploys and worker fetch; direct API/dashboard uploads, rollback with changed secrets, legacy compatibility, and separate-connection deploy-versus-delete/update races. HTTP and cron fail closed on invalid required bindings.
- Evidence: `.scratch/native-secrets-task09-verification.md`; focused race-enabled validation/activation regressions, separate-connection mutation interleavings, real QuickJS HTTP/cron and actual production raw-API/headless-dashboard ZIP uploads prove enforcement, rollback against current values, legacy compatibility, fail-closed invocation and staged/compiled cleanup.

### 10 — Dashboard provisioning before first deployment

- [x] Render environment editor for owned empty sites independently of hasWorker; keep worker-only tabs appropriately gated.
- Display safe required-name/type errors, preserve classification, never source production forms from local dotenv. Keep ZIP-only upload and ownership rules.
- Proof: actual dashboard provisions a required secret on an empty site, uploads first declaration-bearing ZIP, and fetches working deployment; list/update/delete continue working. Capture visual verification and run changed frontend checks/build.
- Evidence: `.scratch/native-secrets-task10-verification.md`; actual headless dashboard provisions required secrets before first deployment, displays safe missing/type errors, uploads the first declaration-bearing ZIP and fetches real QuickJS output. Secret/plain list/update/delete, ownership denial, ZIP-only selection and visual captures passed. TypeScript/build pass; existing unrelated Svelte-check diagnostics remain recorded.

### 11 — Capability negotiation and secure typed client

- [x] Advertise a versioned secret/environment capability on existing version endpoint only after tasks 01–10 enforce the complete server contract.
- Add `internal/client/env.go` typed methods using existing auth/HTTP/error helpers; preserve existing version fields and migrate all affected response consumers.
- Require HTTPS for secret writes, with explicit loopback development exception; prevent unsafe redirects of secret bodies or authorization. Never expose request/response bodies in CLI errors.
- Require capability before secret mutations and manifest-bearing uploads; reject old servers with upgrade guidance. No per-name fallback for bulk.
- Proof: httptest old/new servers, remote HTTP rejection, allowed explicit loopback, redirect refusal, safe errors and atomic bulk; exercise the actual client against the disposable new server.
- Evidence: `.scratch/native-secrets-task11-verification.md`; race-enabled typed-client/actual-handler checks and production-server/real QuickJS smoke prove exact capability gating, old-server rejection, HTTPS/explicit-loopback policy, all-redirect refusal, value-free errors, atomic bulk/storage rollback and negotiated manifest uploads.

### 12 — Native secret commands

- [ ] Register secret put/sync/list/delete; reuse ResolveSite and existing credential resolution/ambiguity handling. No linking or saved target.
- Put resolves server/site before prompting, shows safe target, uses Cobra streams and hidden terminal input; non-TTY requires explicit --stdin. Bound input, remove exactly one trailing LF/CRLF, preserve other whitespace/multiline; no --value.
- Sync requires named bounded dotenv input, rejects local .env, validates complete source/schema before mutation, never saves source/path. Reuse shared server limits. Put/sync support --schema and active remote rules.
- List secret metadata only, sorted names, IDs/flags and safe --json. Delete resolves site-scoped secret name, refuses plaintext, honors existing confirmation and --yes.
- Proof: built CLI against disposable server; real PTY hidden-input capture, piped CI input, missing auth/site/non-TTY errors, exact whitespace, atomic sync, stable IDs/unrelated-name preservation, redacted output and delete confirmation.

### 13 — Local and remote env check

- [ ] Register env check with --schema; local root .env plus explicit shell overrides, no network or application launch/writes.
- Remote --site sends canonical declarations only to validation endpoint; never reads local dotenv values/build-shell values for the request. No mutation.
- Proof: actual CLI local checks with network unavailable; absent/empty/type/optional cases; captured remote request contains no unique local marker and server rejects plaintext bindings for secret declarations.

### 14 — CLI manifest packaging and deploy preflight

- [ ] Add deploy --schema; use invocation-directory schema even when deploying dist, canonical JSON in memory, exclude selected source from ordinary packaging, reject separately supplied canonical conflicts.
- Require capability and read-only remote preflight before manifest-bearing upload; final authority remains server activation transaction. No dotenv read/upload, secret sync, application build, or browser injection.
- Preserve case-insensitive dotenv/symlink exclusions and unchanged schema-less legacy uploads.
- Proof: built CLI deploys dist using either JSON/YAML and explicit schema selection; inspect ZIP for canonical declaration only/no values; old-server rejection, missing-secret failure, success and unchanged legacy upload.

### 15 — Complete release acceptance and documentation

- [ ] Run every row of the source plan's acceptance matrix; record commands, observations, and unavailable coverage in `.scratch/native-secrets-env-verification.md`, marking unrun checks explicitly.
- Launch disposable server/database/key/storage and built CLI. Exercise put/update/sync/list/delete, local/remote checks, failed/successful deploy, rollback, next-invocation updates, and first-deployment dashboard flow.
- Search disposable database secret fields, management responses, captured logs, and fetched public assets for unique markers. Confirm worker receives expected values without intentionally logging/returning raw markers into captured production-like logs.
- Run relevant focused Go packages and `make test` once after integration; Makefile currently uses CGO_ENABLED=1 go test ./.... Run `make test-frontend` and `make frontend`; inspect current frontend scripts for any additional required checks. Exercise QuickJS and V8 using documented prerequisites; explicitly record unavailable engine coverage, never substitute mocked engines.
- Update existing public API/CLI/runtime/architecture docs, docs-site CLI/workers/getting-started pages, server config docs/example, and CHANGELOG with exercised behavior and security limits. Leave README unchanged. Remove development scaffolding.
- Release gate: no active task unchecked, every acceptance row has evidence or an explicitly unresolved blocker; capability must not be presented as complete while server security enforcement is missing.

## Coverage map to original sequential tasks

| Original tasks | Tasks here |
| --- | --- |
| 1–2: declaration and local helpers | 01–02 |
| 3: response safety | 03 |
| 4–5: encryption and maintenance | 05 |
| 6: persistence/coordination | 04 |
| 7–8: single/bulk/validate | 05–06 |
| 9: binding collisions | 07 |
| 10: runtime error propagation | 05, 09 |
| 11: archive and static policy | 08 |
| 12: deployment/runtime/rollback | 09 |
| 13: dashboard | 03, 10 |
| 14: capability/client | 11 |
| 15: secret CLI | 12 |
| 16: env check | 13 |
| 17: packaging/preflight | 14 |
| 18: acceptance/docs | 15; per-task documentation updates |
