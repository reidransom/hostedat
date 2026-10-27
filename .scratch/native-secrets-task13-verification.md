# Task 13 — Local and remote env check

Status: COMPLETE on `feat-secrets-sync`. Commit title: `Add offline and read-only remote environment checks`; exact commit hash returned to the parent. Only task 13/original task 16 checked. No branch, README edit, push, deploy-schema integration or application build.

## Implementation and API guidance

- Registered `hostedat env check [--schema <path>]` and `hostedat env check --remote --site <ID|name|slug> [--schema <path>]` under the new native `env` group. No positional arguments, linking, saved target or new credential storage.
- Both paths call existing command-scoped `secretProject`/`envschema.Discover` once. Require a selected declaration, including explicit `{}`; missing default schemas fail with guidance. Existing invocation-directory-only discovery, both-default conflict, explicit relative paths and `.json`/`.yml` rules remain unchanged.
- Local mode calls only `Project.LocalValues(os.LookupEnv)` then `Schema.Validate`. The root dotenv belongs to the selected schema's directory, not an arbitrary deployment directory. Missing dotenv is allowed; explicitly present shell values override it, including empty strings. Only declared shell names are inspected. All existing parser bounds, literal substitutions, quote/CRLF/multiline/whitespace rules and value-free diagnostics are reused. Undeclared local bindings are ignored; optional means absence, not permission for invalid supplied input. No client/auth/config resolution, writes, parent-environment changes, child app or build.
- Remote mode does not call `LocalValues`, open dotenv, or look up declared shell names. It resolves the existing credential/server/site contract through `secretTarget` and invokes `Client.ValidateEnvVars` with the parsed schema only. The typed client emits canonical JSON with explicit required flags. This is a validation POST, not a write; candidates never activate deployment requirements. No exported client/schema interfaces changed.
- Remote requires HTTPS, the exact server capability and redirect refusal; `--allow-insecure-loopback` is explicit development-only opt-in. `--remote` requires `--site`. Supplying `--site` or the transport exception in local mode fails rather than silently validating only local data for an intended remote target. A false successful-response `valid` flag also fails closed.
- Local success reports a declaration count; remote success reports a quoted resolved site ID/count. Errors never print values or arbitrary response bodies; existing typed-client server failures intentionally remain status-only. All declared names, including VITE_ names, require secret classification remotely.
- Task 14 can reuse `secretProject`, the existing typed client's `ValidateEnvVars`, and its canonical schema/client contracts for manifest preflight. No dotenv loading belongs in deploy.

## Focused verification executed

Implementation and permanent consumer tests were finished before running:

```sh
gofmt -w cmd/hostedat/env.go cmd/hostedat/env_test.go cmd/hostedat/main.go
go test -race ./cmd/hostedat ./internal/client ./internal/envschema
go build -o /tmp/hostedat-task13 ./cmd/hostedat
go build -o /tmp/hostedat-task13-server ./cmd/server
```

All passed: CLI 1.061s; client and envschema cached passes; both production binary builds exit 0. No product changes after these checks; subsequent changes are documentation/evidence only.

Permanent command tests cover missing/empty required values, invalid numbers, optional absent/invalid/empty-string cases, explicit empty shell precedence, valid shell replacement, ignored undeclared values, quotes/hash/CRLF/multiline/literal substitution, safe diagnostics, missing schema, invalid mode/positional arguments, explicit YAML/root association, conflicting default schemas and explicit empty JSON. The local-path HTTP sentinel saw zero requests and the test directory acquired no files. Remote tests use an unopenable directory as `.env`, a unique declared shell marker, exact canonical request-body/auth assertions, success/false-valid/error responses and value-free output. These HTTP contract tests are not presented as real-engine proof.

## Actual built CLI with network unavailable

A disposable tool-session Python harness used an inherited Linux libseccomp filter returning EPERM for `socket`, `connect`, `sendto` and `sendmsg`. A separately executed Python `socket.socket()` probe confirmed `Operation not permitted`. Initial curl probing returned its generic connection-failure message rather than errno text, so the diagnostic-only probe assertion was corrected to the explicit socket probe; no product code/check was changed or skipped.

Executed the real `/tmp/hostedat-task13 env check` under that filter in a temporary invocation directory, with no credentials, an unreachable server setting and a nonexistent HOME/config directory. Nine actual cases passed:

1. Absent required string: exit 1, missing-value diagnostic.
2. Empty required dotenv string: exit 1, empty-value diagnostic.
3. Explicit empty shell override over nonempty dotenv: exit 1, empty diagnostic.
4. Unique marker as a number: exit 1, number constraint without marker disclosure.
5. Shell `7` overriding that invalid number: exit 0.
6. Absent optional boolean: exit 0.
7. Invalid supplied optional boolean: exit 1, boolean constraint without marker.
8. Quoted hash, literal `${TASK13_NO_EXEC}`, significant spaces, physical multiline value and CRLF: exact enum match, exit 0. Unrelated marker binding ignored.
9. Explicit empty declaration with missing dotenv: exit 0.

Every run preserved input file bytes and mtimes, created no additional files or HOME/config directory, and omitted the unique marker from stdout/stderr. The inherited filter made network syscalls unavailable rather than relying only on an unreachable endpoint.

## Actual production-server remote checks and real QuickJS

The harness created disposable 0600 operator key material, SQLite/config/storage under `/tmp/hostedat-task13-*`, then ran:

```sh
/tmp/hostedat-task13-server --config TEMP/config.yaml --secrets-maintenance migrate --writers-stopped
/tmp/hostedat-task13-server --config TEMP/config.yaml
```

Migration passed. Server ran on ephemeral literal-loopback HTTP with real default QuickJS, domain `secret.test`; CLI used explicit `--allow-insecure-loopback`. Registered a disposable account and created an empty site through actual API.

A recording loopback HTTP forwarder captured requests and forwarded them to the real server without simulating validation or changing responses. A local YAML declaration required `TASK13_REMOTE`/number. Both associated `.env` and the declared shell variable contained a unique nonnumeric local marker. Five built-CLI validation POSTs were captured:

1. Existing stored `TASK13_REMOTE` was plaintext `7`: command exit 1/HTTP 400, proving plain classification cannot satisfy a secret declaration despite a valid numeric value.
2. Repeated plaintext rejection with database snapshot comparison: environment/site/deployment rows unchanged.
3. Explicit API replacement as secret `7`: native remote check exit 0. Snapshot comparison again unchanged.
4. Replaced local dotenv with malformed marker-bearing syntax: remote check still exit 0, proving it was not opened/parsed; invalid declared shell marker likewise had no effect. Snapshot unchanged.
5. Uploaded a canonical declaration-bearing ZIP and marker-free worker via actual production API. Real QuickJS fetch returned `200 binding-ok` after checking the value is a string equal to `7`. Another native remote check passed without modifying environment/site/deployment rows; the subsequent real invocation still returned `200 binding-ok`.

All five POST bodies were exactly:

```json
{"schema":{"TASK13_REMOTE":{"type":"number","required":true}}}
```

Other captured requests were only capability/version and site-list GETs. No PUT/DELETE, environment value writes or upload came from `env check`. Local marker was absent from every captured body, all captured CLI output/errors and production logs. No values or dotenv/source paths were part of the validation request. Database comparisons were on actual persisted rows, not mocked state.

Stopped the production server with SIGINT, exit 0; shut down the forwarding server. Removed disposable database, keys, storage, inputs, captures/logs and both binaries. Orchestration existed only in the tool session; no throwaway repository script/scaffold remains.

## Documentation and remaining risks

After proof, updated existing public CLI reference, docs-site CLI page and CHANGELOG. README unchanged. Updated only this task's proven checkboxes/evidence.

No task-13 blocker. No application UI changed, so no browser/UI smoke was needed or claimed. Real QuickJS is exercised above; V8, full suite, frontend/docs release builds and complete acceptance remain task 15, while manifest packaging/preflight is task 14. Declaration enum literals are metadata and travel with the declaration; local binding values are never loaded for remote requests. Authorized workers can disclose their own values and in-flight snapshots retain old strings; environment checks do not alter those established security limits.
