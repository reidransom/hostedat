# Task 10 — Dashboard provisioning before first deployment

Status: COMPLETE on `feat-secrets-sync`. Commit title: `Enable dashboard secret provisioning before first deployment`; exact hash returned to parent. Tasks 11–15 remain unchecked. No branch, README change, capability advertising, CLI implementation or push.

## Implementation/API guidance

- `WorkerPanel.svelte` always renders Env Vars. Empty/static-only sites show the provisioning explanation and only the Env Vars subtab; KV, D1, Durable Objects, Crons and Logs remain gated by `hasWorker`. The existing parent site fetch and owner/admin API authorization are unchanged.
- `EnvVars.svelte` explains explicit production-value entry, Secret selection for declarations and artifact-root `_hostedat.env.json` packaging. It does not read local files or dotenv. Reuse task 03's discriminated DTO, password input, list-only placeholder and locked classification for existing names; no duplicate environment API or new exported interface.
- Existing server errors already flow through deployment inline errors and environment error toasts. Actual missing-name/type/delete errors below contain names/reasons only. No new value-bearing error interpolation was introduced.
- Found an existing dashboard/server mismatch: the dashboard offered `.tar.gz`/`.tgz` although the upload contract is ZIP-only. Restricted file acceptance, copy and validation to `.zip`. Selecting an unsupported file also clears any previously selected ZIP, preventing an accidental stale upload. Server upload behavior is unchanged.

## Frontend checks executed

After the implementation, ran:

```sh
make test-frontend && make frontend
npm run check # cwd: web
make test-frontend && make frontend && go build -o /tmp/hostedat-task10-server ./cmd/server
```

The first check/build passed; final check/build/server build after the ZIP-only correction also passed. `make test-frontend` runs `npx tsc --noEmit`; `make frontend` builds the real Svelte dashboard embedded by the server. Production build reports only the three established Svelte initial-prop warnings and Node's module.register deprecation warning.

Whole-app `npm run check` remains failing with the same **seven errors and three warnings in unchanged files** recorded by task 03, none in the three changed components:

- `(app)/+page.svelte:45`: unknown `action` prop.
- `(app)/admin/invites/+page.svelte:122` and `(app)/keys/+page.svelte:255,271`: unknown `onclose` prop.
- `(app)/sites/[id]/+page.svelte:43,54,55`: route parameter `string | undefined` passed to string argument.
- Warnings: initial prop captures in `SiteSettings.svelte:16,17` and `StoragePanel.svelte:21`.

No Go implementation changed; no full Go suite, V8 run or mocked-engine substitute. Full-suite/release integration remains task 15.

## Actual production server/headless dashboard smoke

Only disposable credentials/data were used. Built the production binary above, provisioned an operator-owned 0600 random key JSON and config, and ran:

```sh
/tmp/hostedat-task10-server --config /tmp/hostedat-task10-bwgg0fzf/config.yaml --secrets-maintenance migrate --writers-stopped
/tmp/hostedat-task10-server --config /tmp/hostedat-task10-bwgg0fzf/config.yaml
```

Offline migration exited 0 with zero secrets verified. Server listened on `127.0.0.1:49799`, domain `provisioning.test`, default real QuickJS engine. Random throwaway owner registered (201), and site `1s5uawvjvzf3` was created (201), initially `has_worker:false`, `active_version:null`.

Used actual embedded production dashboard in managed headless Chromium, no desktop window, injected management requests, response stubs or mocked frontend:

1. Signed in through the login form, opened the owned empty site's Worker tab. Env Vars and its inputs were visible, the first-deployment explanation was present, and the KV subtab count was zero. Visual: `native-secrets-task10-empty-dashboard.webp`.
2. Entered TOKEN and COUNT explicitly through the environment form and selected Secret. Actual writes returned 200, `Cache-Control:no-store` and metadata with `secret:true`, no value. COUNT was `7`; TOKEN used a unique disposable marker. Actual site GET still showed no active deployment and `has_worker:false`. List rendered local bullets, not either value. Visual: `native-secrets-task10-provisioned-dashboard.webp`.
3. On Deploy, actual file input accepted only `.zip`. Selected a valid ZIP, then selected `unsupported.tgz`: UI showed `File must be a .zip archive` and removed the upload action for the previous ZIP (only the parent Deploy tab button remained).
4. Uploaded `missing-required.zip` with an additional MISSING_REQUIRED declaration through the actual file input/Deploy button. HTTP 400 and inline `environment variable MISSING_REQUIRED: required value is missing`; site remained empty. Supplemental visual captured at `/tmp/omp-sshots-15b5e999ef0b8687.webp`.
5. Updated staged COUNT through UI to an invalid secret marker before any deployment existed. Uploading the intended first ZIP returned 400 and inline `environment variable COUNT: must satisfy number constraint`, without the supplied marker in rendered text. Supplemental visual: `/tmp/omp-sshots-15b5e9a56c4b8688.webp`.
6. Corrected COUNT to `7` through UI and uploaded `first-worker.zip`. Actual response 201, version 1. ZIP contained `_worker.js`, `index.html` and canonical root declarations for required string TOKEN/number COUNT. Real HTTP fetch with `Host: first-provision.provisioning.test` returned **200 `provisioned-ok`** after worker equality/type comparisons, never the marker. The Worker tab now showed KV and the other worker-only subtabs. Visual: `native-secrets-task10-live-dashboard.webp`.
7. Entering existing TOKEN left the replacement input empty, password type, Secret checked and disabled, with classification-preservation explanation. Updated TOKEN and COUNT (`8`) through actual UI: both 200, no-store, metadata only. Next actual QuickJS request returned **200 `updated-ok`**, with no redeploy.
8. Invalid active COUNT replacement returned 400 `environment variable COUNT: must satisfy number constraint`; actual toast displayed the safe error, not the marker. Required COUNT delete through UI returned 400 `environment variable COUNT: required secret cannot be deleted` and retained its row.
9. Created TEMP_SECRET through UI (200), then deleted through its accessible delete button. Subsequent real list and DOM confirmed removal. Created EMPTY as plain empty string, updated it to empty again (both 200, same ID, `secret:false,value:""`), then deleted (204). Final secret list contained only TOKEN/COUNT with their original IDs, `secret:true` and no `value` keys. Existing secret/plain classification was preserved throughout.
10. Registered a separate normal user. Real site and env list calls both returned 403. Signed into a second actual dashboard tab as that user; direct navigation to the owner site showed `access denied` and no environment form. Existing ownership policy remains intact.
11. Disposable SQLite showed exactly one successful deployment, active ID matching v1 and enforcement flag 1. Both live secret rows had plaintext length 0, version 1, and nonempty ciphertext (TOKEN 63 bytes, COUNT 17 bytes). Captured management responses and rendered text did not contain the marker; specialized grep of `proc://task10-server` logs for `private-task10` returned no matches. This is targeted smoke, not the final all-surface leak acceptance audit.

Browser-harness adjustments were necessary: waited for the actual post-login page rather than racing navigation; waited for loaded environment rows before interacting with the classification checkbox; used raw page response capture because the Python wrapper does not accept JS `args`; treated DELETE 204 as bodyless; corrected an assumed delete-error wording to the observed `required secret cannot be deleted`. Early harness wait/selector/body-read failures were not product failures or substituted assertions. No extra deployment was activated by those attempts.

Closed both browser tabs. Stopped the service with SIGINT (exit 0). Removed the disposable binary, config, database, key, ZIPs and storage directory. No smoke script or application scaffold remains. Retained only the requested scratch visual evidence/report.

## Documentation and remaining risks

Updated existing worker-runtime reference, docs-site workers page and CHANGELOG after proof. Both scratch plans mark only task 10/original task 13 newly complete.

- No unresolved task-10 functional blocker. Whole-app Svelte check still has the established unrelated diagnostics above; TypeScript and production frontend/server builds pass.
- Required names/types are surfaced through safe server errors, not an additional schema browser. Users must select Secret for newly entered declaration names; existing names keep their classification. No secret reveal/export or local dotenv ingestion exists.
- Real engine coverage here is QuickJS HTTP. V8, full suite, comprehensive acceptance and CLI workflow remain later tasks. An authorized worker can still expose a value intentionally; at-rest encryption does not prevent that.
