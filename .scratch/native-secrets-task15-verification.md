# Task 15 — Complete release acceptance and documentation

Status: COMPLETE (acceptance execution and documentation), with explicitly unresolved additional tooling limitations below. Commit title: `Document and verify integrated native secrets release acceptance`; exact commit hash is returned to the parent. No product-code change, branch, README edit, push or PR.

## Deliverable and proof

The full 16-row source acceptance matrix, exact commands/outcomes, real engine/browser observations, marker-audit boundaries, fixture corrections, unrun checks and limitations are recorded in `native-secrets-env-verification.md`. That report distinguishes current integrated verification from supplemental earlier task evidence; tests using httptest are not presented as engine/browser proof.

Executed `make test` exactly once after integration: **all project packages passed**, exit 0 (143.32s). Focused ordinary Go packages passed. CLI/server builds passed; V8 requires `CGO_ENABLED=1 CGO_CFLAGS=-std=gnu17` with current v8go on this C23-default toolchain. Actual production QuickJS and V8 HTTP execution and real-engine cron/HTTP failure regressions passed. Frontend `make test-frontend && make frontend` passed; updated Astro docs built with `make docs`.

Real disposable acceptance exercised:

- Built native help, explicit stdin puts, metadata JSON list, replacement preserving IDs/unrelated names, and real PTY hidden input with ECHO disabled/no credential captured.
- Atomic schema-backed named-file merge, local-dotenv refusal, invalid optional entry refusal, and an actual SQLite-trigger storage failure leaving prior COUNT untouched. Optional deletion succeeds; active required deletion fails.
- Eight real network-denied local checks, declaration-only captured remote validation excluding local dotenv/shell markers, production plain-binding classification refusal and all-operation cross-user/wrong-site authorization.
- Missing-secret failed CLI preflight with zero deployment rows; successful v1/v2 deploy; actual rollback to v1 against current updated values; actual QuickJS next-invocation digest/string checks without raw marker output.
- Actual embedded dashboard sign-in, secret provisioning while no worker exists, first declaration-bearing ZIP upload, real QuickJS fetch and gated-tab transition. Two visual captures retained in scratch.
- Live encrypted-field scan of three secret rows, nine extracted/compiled files, CLI/errors, management responses and QuickJS/V8 logs. Final 31 captured response buffers and 19 CLI buffers marker-free. Worker code never returned/logged raw markers; expected values were checked through in-memory digest comparisons and nonsecret type/count indicators.
- Clean production restart from QuickJS to V8 using identical data/key/storage; both workers executed. Actual live V8 tampering returned generic configuration 500; startup rejected tampered data and independently generated wrong key. Separate-copy real legacy migration cleared live plaintext; actual key rotation succeeded. Permanent regressions additionally cover transactional maintenance rollback, cross-binding ciphertext authentication, duplicates, and separate-connection activation/mutation races.

All disposable processes/tabs, binaries, keys, databases, uploads, capture/log files and temporary visual originals were removed. No repository smoke script or application scaffold was retained. Only requested reports and marker-free scratch screenshots remain.

## API/client/runtime guidance

- Resolve the declaration once per command with `envschema.Discover`; `Project.Schema == nil` means absent, whereas `{}` is an explicit empty declaration. Only local `env check` reads associated root dotenv and shell overlays. JSON/YAML canonicalize to the same flat JSON rules. Never put credentials in declaration fields/enum literals.
- `Client.Deploy(siteID, dirPath, project)` accepts the command-scoped project; pass nil for legacy schema-less uploads. Manifest uploads require capability `secrets-env-v1`, secure transport, declaration-only preflight and final server transactional validation. There is no bulk sequential fallback or secret export. Use hidden put/stdin, not value arguments.
- Every declared value remains a target-side secret, including `VITE_` names. Native put/sync send secret classification. API public DTOs omit secret values rather than placeholders; preserve valid empty plain strings. Environment responses use no-store. Candidate schemas validate but never activate.
- First manifest activation requires future manifests; explicit empty rules can remove requirements without re-enabling absent manifests. Rollback checks current secrets and does not restore historical values. `BuildEnvFromDB` and factories propagate errors; HTTP/cron fail closed. Fresh invocation string snapshots see updates; in-flight requests/WebSockets retain old bindings.
- Provision operator-owned 0600/0400 random key JSON separately from database/artifacts. Fresh databases also require the offline migration/uniqueness initializer. Stop every writer/worker; acknowledgement is not a supervisor. Restore consistent database plus matching keys; old plaintext-reading binaries require coordinated pre-cutover restore. Never regenerate a replacement key to recover ciphertext.
- Require production HTTPS at server/trusted termination. Explicit loopback HTTP is development only. At-rest protection does not prevent authorized workers/admins from disclosure, erase old backup/WAL history, zeroize memory or scan renamed/bundled secrets.

## Documentation updated

After exercised behavior was established, updated existing:

- `docs/public/{api-reference,cli-reference,worker-runtime,architecture}.md`;
- docs-site `cli`, `workers`, `getting-started`, `configuration`, and `server` pages;
- `config.example.yaml` (fresh initialization, key backup warning, accurate QuickJS/V8 prerequisites);
- `CHANGELOG.md` (integrated acceptance and removal of obsolete pending-cutover wording).

Architecture now identifies the actual external worker module and SvelteKit dashboard rather than the obsolete local-worker/React descriptions. README intentionally untouched. No exported code/API contract changed in task 15.

## Remaining risks and explicitly unrun checks

1. Additional whole-app `npm run check` still fails with seven established unrelated typing errors: PageHeader `action`; three Dialog `onclose`; three possibly undefined route-ID arguments. Three existing initial-prop warnings remain. TypeScript and production frontend build passed, but **not all frontend checks are green**. These unrelated component contracts were intentionally unchanged, as in tasks 03/10.
2. Initial broad `-race` command also included an obsolete nonexistent local worker path and hit upstream QuickJS checkptr VM-internals pointer arithmetic in API/adapter. Correct ordinary focused packages/full suite passed. No instrumentation was suppressed. Both real engines ran successfully, so engine coverage is not unavailable; whole-package QuickJS race/checkptr coverage remains unavailable. Full V8 module suite/race suite was not run; named real HTTP/cron/dispatcher tests and actual production V8 smoke were run.
3. Historical released server/CLI binaries, external production HTTPS, other architectures, load tests, production migration and secure-erasure guarantees were not exercised. Compatibility/transport/collision/archive edge cases are covered by current permanent consumer tests; prior real two-process/API/UI/maintenance experiments remain explicitly supplemental.

No unresolved native-secrets server security-enforcement blocker was found. Task checkboxes represent the completed acceptance execution/documentation deliverable, not a claim that the separately recorded upstream/unrelated tooling limitations vanished. Parent owns publishing and any policy decision about whether those known diagnostics block release.
