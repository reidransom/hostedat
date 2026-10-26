# Task 09 — Deployment, rollback, and invocation enforcement

Status: COMPLETE on `feat-secrets-sync`. Commit title: `Enforce declarations on deployment rollback and invocation`; exact hash returned to the parent. Tasks 10–15 remain unchecked. README unchanged; capability remains disabled.

## Implementation/API guidance

- Reuse the canonical nullable snapshot accepted by task 08. `nil` is absent; `"{}"` is an explicit empty declaration. No values are introduced into manifests or management deployment responses.
- `DeployHandler` receives the established shared environment service from route wiring. It checks the fresh site's enforcement state and validates required presence, secret classification, decrypted types and all declaration-name collisions in a read-only pre-compilation check, then repeats validation inside `models.WithSiteTransaction` immediately before creating/activating the deployment.
- The activation transaction atomically preserves or sets `EnvDeclarationsEnforced`. Successful first manifest activation opts in; later manifest omission fails, including static-only uploads. Explicit empty declarations remove requirements without disabling enforcement. Unenforced legacy schema-less deployments/rollbacks continue working.
- Rollback loads its site-scoped target under the same lock, validates the target against current decrypted bindings, and only then switches activation. No old secret values are restored. Enforced sites reject schema-less historical targets.
- All post-extraction failure paths share deferred retirement of staged files/bytecode and `InvalidatePool` (which also removes upstream cached sources). Old pools are invalidated after successful activation even when switching to a static deployment. Failed activation does not invalidate the previous deployment.
- `Service.Declaration` caches the latest loaded immutable parsed declaration per site, keyed by deployment ID and document, not values. Invocation loading never rereads a manifest from disk. Switching loaded snapshots replaces the cached entry; historical snapshots are reparsed when loaded again. Values are decrypted anew for every invocation.
- `BuildEnvOptions.DeploymentID` identifies the artifact actually selected for execution; HTTP sets it explicitly. Cron and retained dispatcher factories now have signature `func(siteID, deployID string) (*worker.Env, error)`; all references were inspected/migrated. This avoids validating a newly active schema while executing a previously selected artifact during activation changes.
- `LoadForDeploymentFrom` allows the worker adapter to load schema, live rows and all binding metadata on one database read snapshot. Stores retained in the worker environment use the original live DB handle, not the closed read transaction. HTTP returns only generic configuration failures; cron skips invalid environments without setting `LastRunAt`. Ordinary undeclared plaintext bindings remain supported.

## Focused verification executed

Implementation was finished before formatting/checks. No full suite, frontend build/check or V8 run was performed.

```sh
gofmt -w internal/environment/declarations.go internal/environment/service.go internal/api/deploy_handlers.go internal/api/routes.go internal/api/site_serving.go internal/api/declaration_enforcement_test.go internal/workeradapter/env.go internal/workeradapter/cron.go internal/workeradapter/env_test.go internal/workeradapter/env_integration_test.go internal/workeradapter/environment_test.go cmd/server/main.go
go test -race ./internal/environment ./internal/models
go test -race ./internal/api -run 'TestDeclarationDeployRollback|TestDeclarationActivationRechecksConcurrentMutation|TestDeploy|TestSiteTransaction' -count=1
go test ./internal/workeradapter ./internal/api -run 'TestBuildEnv|TestEnvironmentFailures|TestCronDeclarationRealEngine|TestDeclarationInvocationRealEngine|TestServingEncrypted' -count=1
go build -o /tmp/hostedat-task09-server ./cmd/server
```

All passed: environment 1.488s, models 4.122s, API race checks 24.218s; adapter 0.100s and actual-engine API 0.220s; production binary build exit 0.

Added an additional selected-artifact regression after the first batch, then ran:

```sh
gofmt -w internal/environment/declarations_test.go
go test -race ./internal/environment -run TestInvocationValidatesSelectedDeclaration -count=1
```

Passed in 1.136s. It proves the exact historical artifact's rules are checked even while a different empty declaration is active, invalid decrypted number/optional boolean values fail, valid values remain strings, and cached parsed declarations do not cache values.

Permanent consumer regressions also prove:

- Missing/plain/invalid declared values fail; successful manifest activation sets enforcement; later omitted manifests and schema-less rollback fail; explicit empty schemas can revise requirements; rollback rejects invalid current values and retains corrected current values.
- Separate SQLite connections commit delete/update/binding creation specifically after deploy preflight and during compilation, before the final activation transaction. All three interleavings fail final activation, leave the site unenforced/unactivated, remove bytecode/assets and invalidate the newly compiled pool. A deterministic compiler callback controls this race window only; it is not engine smoke. Existing failed-transaction/prior-active-state and lock-wait tests also passed.
- Actual QuickJS HTTP worker compilation/fetch succeeds with decrypted required strings, fails closed after out-of-band required deletion/plain replacement, and resumes after valid repair.
- Actual QuickJS scheduled execution receives the decrypted string and emits only `cron-binding-ok`. Out-of-band required deletion makes the next tick skip source/execution and leaves the successful `LastRunAt` unchanged. Existing dispatcher error regression passes.
- Existing binding-loader fixtures now include actual Site/Deployment tables and owned legacy sites, matching the new invocation lookup contract.

Actual-engine checks run without race instrumentation because of the established upstream QuickJS checkptr incompatibility. Storage/model/activation tests retained race coverage. No mocked engine is substituted for HTTP/cron smoke.

## Actual production server and dashboard smoke

Used only disposable `/tmp/hostedat-task09-*` database, operator key, config, archives, storage and binary; random throwaway account credentials. No smoke script or application scaffold was retained.

1. Executed real offline `--secrets-maintenance migrate --writers-stopped --config PATH`: exit 0, zero-secret database verified. Started the built production server on loopback port 49533 with `domain: enforcement.test` and its default QuickJS engine. Registered an owner and created a site via real HTTP: both 201.
2. A schema-less legacy worker ZIP deployed as v1 (201) and served a real fetch. A required COUNT declaration with no stored value returned 400, names-only `required value is missing`.
3. The first rapid request batch hit the existing deploy-route limiter (429), despite zero rate-limit configuration; those throttled calls are not claimed as enforcement checks. Spaced subsequent uploads by 1.1 seconds. Plain COUNT returned 400 `must be a secret`; encrypted invalid COUNT returned 400 `must satisfy number constraint`; valid COUNT allowed manifest-bearing v2 (201). Omitting the next manifest returned 400 `deployment requires an environment declaration`.
4. Rollback to schema-less v1 returned 400. Explicit empty manifest deployed as v3 (201); changing COUNT to an invalid number then rolling back to v2 returned 400. Correcting COUNT to a different valid string `9` allowed rollback (200); actual worker fetch compared against the current expected value and returned only `binding-ok`, proving no historical value restoration.
5. SQLite showed active v2, enforcement flag 1, and exactly v1 NULL/v2 canonical number declaration/v3 `{}` snapshots. Storage had exactly three successful directories after all failed API uploads.
6. Opened actual embedded dashboard in headless Chromium (no desktop window), signed in through its login form, navigated to the owned site's Deploy tab, selected `dashboard-missing.zip` through the actual file input and clicked Deploy. UI showed `environment variable MISSING_UI: required value is missing`, retained v2 and did not activate the failed artifact. Visual screenshot: `/tmp/omp-sshots-15b5e81ba0a133dd.webp`.
7. Selected the valid declaration-bearing ZIP through the same UI and clicked Deploy. Dashboard advanced to v4 and cleared the selected file; SQLite confirmed `(4, 1)`. A real QuickJS fetch of the dashboard-deployed artifact returned 200 `binding-ok` for current COUNT `9`. Visual screenshot: `/tmp/omp-sshots-15b5e824ace133de.webp`. No injected upload API or mocked frontend was used. Login initially navigated to `/`, not the harness's assumed `/sites`; corrected observation/navigation. Browser selector attempts using a nonexistent submit type/unsupported locator method were harness-only and did not submit extra uploads.
8. Deleted required COUNT directly in disposable SQLite to simulate operator/out-of-band damage. Production HTTP returned 500 with only `worker environment configuration failed`; valid guarded API repair returned 200. Final storage directory count was four, matching only successful deployments. Live secret fields showed `secret=1`, empty plaintext `value`, format version 1 and ciphertext length 17.
9. Every captured management response omitted the unique invalid secret marker; a specialized grep of production service logs for `private-task09-marker|invalid-private-marker` found no matches. Worker smoke returned only equality/type outcomes, not secret markers.
10. Closed the browser tab; stopped server with SIGINT (exit 0). Closed SQLite and removed disposable database, keys, config, ZIPs, storage and binary. No throwaway scaffolding remains in the repository.

## Documentation and remaining risks

Updated existing API/runtime/architecture docs and CHANGELOG after proof. Marked task 09 and original-plan task 12 complete. No capability advertising, native CLI, dashboard provisioning changes, README edits or branches.

- No unresolved task-09 blocker. Out-of-band database operators can invalidate bindings; invocation now fails closed but does not repair their data.
- An in-flight invocation retains its previously loaded environment; activation/secret updates cannot revoke strings already handed to worker code. At-rest encryption cannot stop an authorized worker from logging/returning secrets.
- Cache retains only the latest parsed snapshot per site, not values or an unbounded deployment history. Different concurrently selected artifacts can cause reloads, not stale-value reuse.
- Real engine coverage is QuickJS HTTP and scheduled invocation. V8, complete acceptance/full suite/frontend release checks remain task 15. First-deployment dashboard secret provisioning remains task 10; this task exercised the existing actual upload flow on an owned deployed site.
