# Native secrets/environment — integrated release acceptance

Status: acceptance execution COMPLETE. All 16 source-plan matrix rows have passing behavioral evidence below. This does **not** mean every optional/additional tool check is green: whole-app Svelte checking still has seven established unrelated errors, and upstream QuickJS does not support Go checkptr instrumentation. Full non-race Go suite, focused packages, frontend TypeScript/build, docs build, actual QuickJS and actual V8 execution passed. No missing server security-enforcement coverage was discovered. Task-specific implementation/API guidance is in `native-secrets-task15-verification.md`.

## Commands actually executed

After tasks 01–14 had landed, with no product-code edits during verification:

```sh
# Initial broad race attempt (failed; see limitations below):
go test -race ./cmd/hostedat ./internal/client ./internal/envschema ./internal/environment ./internal/models ./internal/api ./internal/storage ./internal/workeradapter ./internal/worker/...

# Full integration suite, exactly once:
make test
# Makefile expands to: CGO_ENABLED=1 go test ./...

make test-frontend && make frontend
# web: npx tsc --noEmit; npm run build
npm run check # cwd web

go build -o /tmp/hostedat-task15 ./cmd/hostedat
go build -o /tmp/hostedat-task15-server ./cmd/server
CGO_ENABLED=1 go build -tags v8 -o /tmp/hostedat-task15-server-v8 ./cmd/server
# Initial V8 C23 header failure; -std=c17 then failed missing GNU libc declarations.
CGO_ENABLED=1 CGO_CFLAGS=-std=gnu17 go build -tags v8 -o /tmp/hostedat-task15-server-v8 ./cmd/server

go test ./cmd/hostedat ./internal/client ./internal/envschema ./internal/environment ./internal/models ./internal/api ./internal/storage ./internal/workeradapter
CGO_ENABLED=1 CGO_CFLAGS=-std=gnu17 go test -tags v8 ./internal/api ./internal/workeradapter -run 'Test(ServingEncryptedEnvironmentFailsClosed|DeclarationInvocationRealEngine|CronDeclarationRealEngine|EnvironmentFailuresStopDispatch)$' -count=1 -v

# After exercised-behavior documentation updates:
make docs
```

Observed outcomes:

- `make test` passed every project package, exit 0, 143.32 seconds. API 141.312s, SeaweedFS package 31.849s, workeradapter 0.188s. No package failed or was filtered out. Dependency packages outside this module are not included by `./...`.
- Focused ordinary Go command passed all eight packages, using the full-suite cache. Race attempt separately passed CLI/client/schema/environment/models; API and workeradapter hit upstream QuickJS checkptr. The nonexistent `internal/worker/...` argument was an orchestration mistake: runtime implementation now lives in `github.com/cryguy/worker/v2`; no missing local package was invented or stubbed. Correct scoped command above succeeded.
- Frontend TypeScript and production dashboard build passed, exit 0. Three existing Svelte initial-prop warnings and Node module.register deprecation remain.
- Extra `web/package.json` script `check` was inspected and run. It failed with exactly seven established errors and three warnings, listed below. It was not replaced with the narrower TypeScript check.
- QuickJS production server and CLI builds passed. V8 built successfully with `CGO_CFLAGS=-std=gnu17`; both real production engines were exercised below. V8 targeted HTTP/cron/dispatcher tests all passed (API 0.153s, adapter 0.026s).
- `make docs` installed locked docs dependencies and built eight Astro pages, exit 0. Browser-data age warning and npm esbuild install-script policy warning were informational; build succeeded.

## Disposable end-to-end execution

Tool-session Python orchestration (not a retained repository script) created `/tmp/hostedat-task15-a4yaqyi2`: operator-owned mode-0600 random AES key file, independent JWT secret, SQLite database, storage/worker directories, invocation projects, named production source, and isolated HOME. Production server domain was `acceptance.test`, literal-loopback HTTP `127.0.0.1:59725`; native secret/manifest commands used explicit `--allow-insecure-loopback`. No real account, key, site or production database was used.

Executed the actual production offline initializer before startup:

```sh
/tmp/hostedat-task15-server --config TEMP/config.yaml --secrets-maintenance migrate --writers-stopped
/tmp/hostedat-task15-server --config TEMP/config.yaml
```

Created disposable owner/API key and empty main/first-deployment sites through real APIs. Actual native command sequence included:

```sh
hostedat --help
hostedat deploy SITE ./dist --allow-insecure-loopback # missing-required preflight failure
hostedat env check
hostedat secret put SITE TOKEN --stdin --allow-insecure-loopback
hostedat secret put SITE COUNT --stdin --allow-insecure-loopback
hostedat secret list SITE --json --allow-insecure-loopback
hostedat env check --remote --site SITE --allow-insecure-loopback
hostedat deploy SITE ./dist --allow-insecure-loopback
hostedat secret put SITE TOKEN --stdin --allow-insecure-loopback # replacement
hostedat secret sync SITE TEMP/production.txt --allow-insecure-loopback
hostedat secret sync SITE PROJECT/.env --allow-insecure-loopback # refused
hostedat secret sync SITE TEMP/production.txt --allow-insecure-loopback # invalid optional boolean, refused
hostedat secret sync SITE TEMP/production.txt --allow-insecure-loopback # forced SQLite failure, rolled back
hostedat secret delete SITE OPTIONAL --yes --allow-insecure-loopback
hostedat secret delete SITE TOKEN --yes --allow-insecure-loopback # required, refused
hostedat secret put SITE TOKEN --allow-insecure-loopback # real hidden-input PTY
hostedat deploy SITE ./dist --allow-insecure-loopback # version 2
# Actual POST /api/v1/sites/SITE/deployments/1/rollback
```

Worker source contained no secret marker. It SHA-256-hashed the actual `env.TOKEN`, returned the digest plus nonsecret version/count/type indicators, and exposed a safe ASSETS forwarding path. The harness compared digests in memory. It did not return or log raw markers into production-like logs. Initial invocation proved expected TOKEN, COUNT `7`, and JavaScript string type; replacement immediately changed the next invocation's digest without redeploying, with all existing IDs preserved. Atomic sync changed COUNT to `8`, preserved omitted TOKEN, and provisioned optional boolean. Invalid input and a real SQLite `BEFORE UPDATE ... RAISE(ABORT,...)` trigger each left COUNT `8`; the trigger was removed. Optional deletion succeeded and required TOKEN deletion failed. Real PTY inspection proved ECHO disabled and captured no marker. First PTY attempt had a transient capability-request failure before prompting; a subsequent actual hidden-entry transaction succeeded. An early prompt matcher expected `Value:` instead of the actual `Secret (hidden):`; evidence uses the actual prompt and terminal mode.

Version 2 activated, then actual rollback to version 1 retained current TOKEN digest and COUNT `8`. Real worker ASSETS requests returned 403/404 for reserved metadata/dotenv and 200 `release-public-ok` for index.html.

Local acceptance additionally executed eight real CLI cases with Linux libseccomp denying `socket`, `connect`, `sendto`, and `sendmsg`: absent required, empty required, empty shell override, invalid marker number, valid shell override, absent optional boolean, invalid optional boolean, and an exact enum containing quoted hash/literal substitution/significant whitespace/physical multiline CRLF. All expected exit statuses matched; marker absent from diagnostics; input bytes/mtimes unchanged; no HOME/config created. No credentials or application launch were required.

A recording HTTP forwarder passed an actual native remote check through to the real server. The captured validation payload contained exactly `schema`, with canonical declarations, and no local dotenv or declared-shell marker. No mock validation response was used. An independent normal user was denied (403) for owner-site list/single/bulk/validate/delete. Deleting an owner-site variable ID through the other user's own site returned 404. A real plain binding with valid string value could not satisfy a secret declaration (400).

### Actual first-deployment dashboard

Managed headless Chromium used the actual embedded production dashboard, normal sign-in form, and actual file input/buttons; no injected API mutations, response stubs or desktop windows. On the owned empty site, Worker → Env Vars was visible while `has_worker:false`. Entered UI_TOKEN explicitly, selected Secret, and verified a local-only placeholder, no marker in rendered text, and still no deployment. Uploaded `first.zip` with `_worker.js`, index.html and canonical root declaration; actual server created v1. Real QuickJS fetch returned `200 first-ok` after checking the binding is a string of the expected length. Other worker tabs appeared only after activation.

Visual evidence: `native-secrets-task15-provisioned-dashboard.webp` and `native-secrets-task15-live-dashboard.webp`. Login/navigation initially raced persistent auth, corrected by waiting for actual Sites content; a success-text wait assumed `Deployed` although the actual completed UI shows `v1` and clears the file. Actual API state/fetch and screenshots establish success, not those erroneous fixture waits.

### Marker audit and both real engines

Three unique disposable markers (initial production secret, replacement, local-only value) were checked without printing them. Final live secret audit covered three rows (main TOKEN/COUNT and dashboard UI_TOKEN): Value empty, format 1, expected key ID, 12-byte nonce, ciphertext >16 bytes; no marker in any inspected secret field. Nine extracted/compiled files, captured management/invocation response bodies (17 at the first consolidated scan), CLI output/error buffers, and QuickJS/V8 production logs were marker-free. Final management lists were `no-store`, secret metadata only with no value fields. Marker checks were repeated for later captured responses/maintenance diagnostics/logs. Raw request bodies for an explicit secret write necessarily contain the supplied value in memory; this is not a claim that plaintext never exists in authorized memory or TLS payloads.

Stopped QuickJS cleanly (SIGINT exit 0) and restarted the exact production database/key/storage under the built V8 binary. Both deployed workers executed successfully: main retained rolled-back version and current expected digest/string type; first-deployment site returned `first-ok`. Real V8 loader/HTTP/cron regressions additionally exercised failure refusal, not a mocked engine.

Tampered the main live ciphertext: V8 returned generic configuration HTTP 500 without execution/marker disclosure. After stopping V8 cleanly, normal production startup refused the tampered database. Restored original ciphertext; startup with an independently generated wrong key under the same key ID also refused. Restored the matching key. On a separate disposable database copy, recreated one legacy plaintext-secret row, ran actual offline migration, verified live Value was cleared and ciphertext contained no marker, then ran actual offline rotation to a newly provisioned restricted key file. Both maintenance commands exited 0. Transaction rollback, cross-binding authentication, duplicate handling, missing-key refusal and recovery details also passed the permanent service regressions in the full suite and retain earlier real-maintenance evidence in task 05.

All services/tabs were closed; all disposable keys/databases/storage/inputs/captures/logs and three binaries were removed. Two marker-free scratch screenshots and reports are retained. No development script, application scaffold, branch, README edit, push or PR was created.

## Source acceptance matrix — every row exercised

“Suite” below means the successful, unfiltered `make test` invocation above; it does not imply that httptest proves a JavaScript engine. Engine proof is explicitly separate.

| Source row | Current integrated evidence / observation |
| --- | --- |
| Native CLI | Actual built help contains secret/env; stdin puts, replacement and real hidden PTY passed. Suite `TestSecretInputBoundaries`, `TestSecretCommands`, source-before-network tests cover missing auth/site/input and safe diagnostics. Prior real PTY/cancellation evidence remains task 12. |
| Local checks | Eight actual network-denied cases above. Suite `TestDotenvLiteralValues`, `TestDotenvDoesNotExecuteSubstitutions`, duplicate/bounded parsing, shell-empty overlay, `TestEnvCheckLocal` and declaration rejection tests cover all specified semantic cases. |
| Put/update | Actual puts/list/ID comparison, digest transition on next real QuickJS invocation, no value in management lists, V8 restart preserves current value. Suite response/client/service tests cover metadata-only write responses. |
| Sync atomicity | Actual successful merge plus invalid source and real SQLite-trigger rollback; omitted TOKEN and IDs retained; active required deletion refused. Suite bulk/service tests cover unrelated plain bindings and whole-batch rollback. |
| Isolation | Actual production other-user denial for all five operations and wrong-site variable-ID 404; suite `TestWorkerEnvBulkAndValidation` and existing owner/admin/scoping tests cover authorization contracts. |
| Storage | Actual encrypted-field/response/log/file scan, live V8 tamper 500 and startup tamper/wrong-key refusal; actual offline legacy migration and rotation. Suite authentication/maintenance tests prove site/name associated-data binding and atomic failures; task 05 retains full real recovery smoke. |
| Declaration rules | Suite strict malformed/duplicate/unknown/grouped/versioned/name/value tests; both-direction and concurrent binding-collision tests; prefix-equivalence test uses VITE_TOKEN. Three actual declared rows classified as secrets. |
| Declaration formats | Suite `TestEquivalentFormatsAndCanonicalRoundTrip`, discovery/conflict/snapshot tests, YAML malformed cases, `TestDeployInvocationSchemaAndPreflight` and client ZIP tests pass. Task 14's inspected real JSON/YAML/explicit-selection uploads remain supplemental historical proof, not claimed repeated manually here. |
| No application build | Actual check/secret/deploy operations only invoked native binaries/server; remote payload declaration-only, local run had denied network and unchanged files. Suite command/ZIP regressions exclude dotenv/shell values and require no application build or browser injection. |
| Remote check | Actual forwarded CLI validation contains only schema and excludes unique dotenv/shell marker. Actual valid plain binding rejected. Suite `TestEnvCheckRemoteDeclarationsOnly` verifies exact bodies and read-only behavior. |
| Deploy | Actual CLI missing-secret refusal leaves zero deployments, correct values deploy; actual dashboard first upload succeeds. Suite declaration deploy/rollback, raw upload/path/preflight/serving/ASSETS cases cover invalid/missing API and dashboard-equivalent ZIPs, dotenv/symlink/disguised/duplicate rejection and failed cleanup. Tasks 08–10 retain supplemental negative real API/UI smoke. |
| Runtime/rollback | Actual QuickJS/V8 production HTTP decrypts string values; actual rollback to v1 retains updated digest/count. Full suite runs real QuickJS cron and HTTP failure tests; explicit V8 HTTP/cron tests pass. Retained dispatcher loader errors pass. No production queue-consumer claim. |
| Concurrency | Suite separate-connection `TestDeclarationActivationRechecksConcurrentMutation` (delete/update/binding), `TestSiteTransactionConcurrentEnvUpserts`, writer-wait and collision tests pass. Task 04/07 retain two-process production-server proof. Not claimed repeated as a new manual two-process run. |
| Regression | Unfiltered full project suite passed, including plain env, bindings, legacy schema-less deploy, failed activation, authentication and site resolution. Existing seven whole-app Svelte typing errors remain an explicitly separate tooling limitation. |
| First deployment | Actual headless dashboard provisioned UI_TOKEN while has_worker false, then uploaded first canonical ZIP; actual QuickJS fetch `first-ok`; V8 restart fetch also `first-ok`; screenshots retained. |
| Mixed versions | Suite capability-gate/version-field preservation/manifest-negotiation tests reject absent or wrong capability without fallback; server raw declaration enforcement remains authoritative. Prior task 14 real forwarding with removed capability is supplemental. No historical released-server binary was run. |

## Explicit unresolved limitations / unrun checks

- Whole-app `npm run check`: seven errors, unchanged from tasks 03/10: `(app)/+page.svelte:45` unknown PageHeader `action`; `admin/invites/+page.svelte:122` and `keys/+page.svelte:255,271` unknown Dialog `onclose`; `sites/[id]/+page.svelte:43,54,55` possibly undefined route ID. Three initial-prop warnings in SiteSettings and StoragePanel. These unrelated UI contracts were intentionally not changed in this acceptance/documentation task. Therefore do not describe all frontend checks as green.
- Initial broad `-race` run did not pass: runtime failure in dependency `worker/v2/internal/quickjs.(*qjsRuntime).tryExtractVMInternals` under checkptr. No instrumentation was disabled to manufacture a pass; normal full suite and both real-engine smoke passed. Full V8 `./... -tags v8` suite and V8 race suite were **not run**; only named real HTTP/cron/dispatcher tests plus actual production V8 restart/tamper smoke were run.
- No historical old server/CLI binary, external public-HTTPS server, multi-platform build, load benchmark, remote production migration, or secure-erasure guarantee was exercised. Compatibility/HTTPS/redirect tests use actual clients and HTTP/TLS test servers; these are transport/contract proof, not an old-binary interoperability deployment.
- Matrix edge cases are deliberately covered by permanent consumer regressions plus the integrated smoke, not a claim to repeat every manual experiment from tasks 01–14. Earlier reports are supplemental and clearly distinguished above.
- At-rest encryption cannot protect against authorized workers/key-holding administrators, memory snapshots, secrets already received by an in-flight request, or old plaintext-bearing backups/WAL/filesystem history. Filename policy does not scan renamed/bundled secrets. Operators must configure HTTPS, keep matching keys/backups separate, coordinate stopped writers, and retire history under their storage policy.
