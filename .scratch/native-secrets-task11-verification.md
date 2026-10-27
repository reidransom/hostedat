# Task 11 — Capability negotiation and secure typed client

Status: COMPLETE on `feat-secrets-sync`. Commit title: `Negotiate encrypted environment capability in secure typed client`; exact commit hash returned to the parent. Tasks 12–15 remain unchecked. No branches, README changes, push, native CLI commands or schema-source packaging.

## Implementation and API guidance

- `GET /api/v1/version` preserves `version` and `min_cli_version` and adds `capabilities: ["secrets-env-v1"]`. Shared `envschema.SecretsCapability` versions the complete encrypted-storage/declaration contract established by tasks 01–10. No support is inferred from a version number. Repository-wide version-consumer search found only existing dynamic API assertions; no older typed response consumer required migration.
- `client.ServerVersion() (*VersionInfo,error)` uses the existing JSON helper without a bearer token. URLs containing userinfo, query or fragment are rejected without echoing them. `RequireSecretsCapability()` checks transport and exact advertised support every time; malformed/missing/unknown support yields fixed upgrade guidance, with no mutation request.
- New typed methods: `SetEnvVar(siteID, EnvWrite, envschema.Schema)`, `BulkSetEnvVars(siteID, []EnvWrite, envschema.Schema)`, `ListEnvVars(siteID)`, `DeleteEnvVar(siteID,varID)`, and `ValidateEnvVars(siteID,envschema.Schema)`. All use the existing HTTP/auth/version/JSON helpers, not a second transport. Bulk sends exactly one bulk request; it never falls back to single writes. Delete remains ID/site-scoped; native secret commands must resolve a name and refuse plain bindings before invoking it.
- `EnvWrite{Name,Value,Secret}` always serializes value and classification, including empty/false. Native secret callers must explicitly set `Secret:true`. `EnvVar` exposes public IDs/name/classification and `*string` only for plain values; unmarshal discards secret values even from a nonconforming response. Empty plaintext strings survive. Bulk responses remain server metadata-only.
- Candidate schemas are serialized with `CanonicalJSON()`. Nil means absent for writes; a non-nil empty map sends explicit `{}`. Validation requires a non-nil declaration and sends only `{schema: ...}`, never local values. Candidate schemas do not activate requirements.
- All environment operations require HTTPS. Explicit `Client.AllowInsecureLoopback=true` permits HTTP only to `localhost` or a literal loopback IP (including IPv6); LAN addresses, wildcard addresses and deceptive localhost suffixes are rejected. This is an API opt-in, not an implemented CLI flag. Later CLI consumers should expose an explicit development opt-in and never derive it automatically from an HTTP URL. No TLS verification is disabled.
- Shared `Client.do` copies the supplied HTTP client (retaining its transport and timeout) and refuses **all** redirects, including same-origin and 301/302/303/307/308, overriding permissive caller redirect policies without mutating their client. This prevents body replay and authorization forwarding. Use the final server URL, not a redirecting alias.
- Environment operations and manifest uploads use status-only `APIError`s and fixed transport/decoder errors. They do not expose arbitrary server messages, bodies, parser text or URLs. Consequently remote validation failures through this client expose HTTP status, not the server's safe names/reasons; local declaration checks can provide detailed value-free diagnostics. Dashboard direct API behavior is unchanged.
- `DeployArchive(siteID,zipData)` accepts an already packaged ZIP and inspects all path components for reserved metadata (including wrappers and case variants) before upload. Manifest-bearing ZIPs require secure transport and capability; failures do not upload the archive. Legacy schema-less `Deploy` delegates to it without capability negotiation, retaining compatibility. `zipDirectory` still refuses manually supplied metadata. Task 14 will supply canonical in-memory source packaging and read-only preflight using this upload boundary; this task does not read dotenv or run application builds.

## Focused verification executed

Implementation was completed before formatting/checks. Initial verification:

```sh
gofmt -w internal/client/client.go internal/client/env.go internal/client/env_test.go internal/client/deploy.go internal/envschema/schema.go internal/api/routes.go internal/api/environment_client_test.go
go test -race ./internal/client ./internal/envschema
go test -race ./internal/api -run 'TestEnvironmentTypedClientAtomicIntegration|TestVersionEndpoint|TestRoutes_VersionEndpoint|TestWorkerEnvBulkAndValidation' -count=1
go build -o /tmp/hostedat-task11-server ./cmd/server
```

All passed: client 1.231s, envschema 2.074s, API 4.205s; production server build exit 0. The existing full client package includes deployment, authentication, sites and storage regressions, so the shared redirect change retained those contracts under focused package verification.

After adding direct public-version URL-credential refusal, final verification:

```sh
gofmt -w internal/client/env.go internal/client/version_test.go
go test -race ./internal/client -run 'TestEnvironment|TestManifestUploadNegotiation|TestServerVersion' -count=1
go test -race ./internal/api -run TestEnvironmentTypedClientAtomicIntegration -count=1
go run ./.scratch/native-secrets-task11-smoke
```

All passed: client 1.181s, actual-handler API integration 1.674s, smoke exit 0. The temporary smoke program was removed afterward.

Permanent tests exercise old servers with absent capabilities despite a high version, unknown versions and malformed capability responses; no mutation or manifest upload is made. They also cover real TLS and explicitly opted-in loopback HTTP, remote HTTP rejection before requests, auth/version headers, preserved version fields, nil versus empty schemas, exact declaration-only validation requests, malicious response-value removal, empty plaintext, fixed errors for echoed markers/malformed JSON/type errors, and no sequential bulk fallback. Redirect tests assert zero destination requests for all five redirect statuses, same-origin and cross-origin/downgrade targets, secret POST and authenticated GET, even with a caller's permissive redirect handler.

The real Echo/SQLite TLS integration exercises typed single/bulk/list/delete/validate methods against actual registered routes, actual encrypted rows and a SQLite trigger that fails the second write after a first update. The stored COUNT remains `7`, invalid batches leave it unchanged, TOKEN upserts preserve its ID, and returned DTOs never disclose secret markers. Existing endpoint tests continue proving preserved version fields and batch behavior.

## Actual production-server and real-engine smoke

Only disposable `/tmp/hostedat-task11-*` configs, database, operator-provisioned random 0600 key, storage and random account credentials were used. The throwaway Go consumer started the built production server after successful real offline migration:

```sh
/tmp/hostedat-task11-server --config TEMP/config.yaml --secrets-maintenance migrate --writers-stopped
/tmp/hostedat-task11-server --config TEMP/config.yaml
```

Server used an ephemeral literal-loopback port, domain `client.test`, real default QuickJS engine, and the new typed client with explicit `AllowInsecureLoopback=true`.

1. Actual registration/site creation succeeded. Public version/capability and `RequireSecretsCapability` succeeded against the production endpoint.
2. One typed bulk request provisioned encrypted TOKEN and COUNT=`7`; an empty plain binding round-tripped. An invalid-name batch and a real second-insert SQLite-trigger storage failure both returned safe errors and retained COUNT=`7`; remote enum validation proved the persisted decrypted value, and no FAIL row existed.
3. `DeployArchive` negotiated and uploaded a real declaration-bearing ZIP containing canonical schema, `_worker.js` and `index.html`. It activated v1. Real HTTP worker fetch returned `200 client-binding-ok` after checking the secret's string type/length and COUNT. The worker source/manifest contained no secret marker.
4. Typed TOKEN replacement preserved ID and returned no value. The next real QuickJS invocation returned `200 bad-binding`, proving it observed the changed secret length without redeploy; neither outcome disclosed a raw value.
5. Real remote schema validation succeeded. Required TOKEN deletion was refused through a safe status-only error. A temporary secret was created/deleted, the plain empty binding was deleted, and final list contained only TOKEN/COUNT metadata with stable IDs.
6. Actual raw management list and typed reserialization contained no unique secret marker. Both live secret rows had empty plaintext, version 1, operator key ID, nonce and nonempty ciphertext; ciphertext did not contain the marker. Captured production logs contained no unique secret marker.
7. The operator-authored SQLite trigger deliberately used a fixed diagnostic `task11-private-storage`. That **static trigger diagnostic does appear in internal GORM error logs**, while it is absent from client errors. It is not a submitted secret value; parameterized SQL logging does not promise to scrub text an operator embeds in database schema/error messages. The first smoke assertion incorrectly conflated that static diagnostic with a secret marker; corrected the probe to test the actual unique submitted marker, and retained this limitation rather than claiming blanket log sanitization.
8. Stopped server with SIGINT (exit 0); closed SQLite and removed all disposable data/binary and the temporary consumer directory. No scaffolding retained.

Harness-only compilation corrections preceded smoke: replaced an unavailable mattn driver import with the repository's existing `models.OpenDB` SQLite implementation and avoided a local config/package name collision. No dependencies changed. These were harness issues, not product failures or mocked substitutes.

## Documentation and remaining risks

Updated existing API/architecture references and CHANGELOG after proof; checked only task 11/original task 14. No unresolved task-11 blocker. Full-suite acceptance, frontend release checks, V8, native CLI operations/PTY coverage, local/remote CLI checks and source-schema deploy packaging remain tasks 12–15. No browser/UI changed or exercised in this task; established task-10 proof remains authoritative.

At-rest encryption cannot stop an authorized worker from disclosing a value. In-flight invocations retain previously loaded strings. Local insecure HTTP is an explicit development exception, never remote permission. Status-only client errors deliberately trade server diagnostic detail for body-independent redaction. Operator-authored database error text can appear in internal logs as noted above; no actual submitted secret marker appeared in this smoke.
