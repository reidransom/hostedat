# Task 14 — CLI manifest packaging and deploy preflight

Status: COMPLETE on `feat-secrets-sync`. Commit title: `Package deploy declarations and preflight stored secrets`; exact hash returned to the parent. Only task 14/original task 17 checked. No branches, README edits, pushes, PRs or application builds.

## Implementation and API guidance

- `hostedat deploy <site> <directory> [--schema <path>]` reuses `secretProject`/`envschema.Discover` once from the invocation directory, regardless of the uploaded directory. Both defaults require selection; explicit missing/unsupported sources fail. JSON/YAML and explicit empty schemas retain their established contracts. Deployment never calls `LocalValues`, reads dotenv or declared shell values, synchronizes secrets, executes an application build or injects browser configuration.
- `Client.Deploy(siteID, dirPath string, project *envschema.Project)` now takes the command-scoped parsed project. All existing callers/tests migrated; pass nil (or a project with nil Schema) for legacy schema-less packaging. The project provides the parsed snapshot plus selected source identity, not local values. There is no compatibility shim or secondary source parser.
- Packaging generates canonical JSON in memory, excludes the selected source and resolved-path/hard-link aliases from ordinary files, preserves case-insensitive dotenv/directory and symlink exclusions, and appends one artifact-root `_hostedat.env.json`. Separately supplied reserved metadata is refused even when equal. Selecting `_hostedat.env.json` itself explicitly as the source is supported; its source bytes are replaced by canonical metadata, not uploaded twice. A changed source after discovery does not change the parsed snapshot.
- `Client.DeployArchive` now handles preflight for every manifest-bearing upload, including callers providing ZIPs directly. It bounded-parses the manifest through `envschema.Parse`, rejects multiple metadata entries, invokes existing `ValidateEnvVars` (which negotiates exact capability and secure transport), and requires `valid:true` before multipart upload. Declaration-only remote validation is read-only; no secret list/value fetch or per-name mutation occurs. The server still owns archive path validation, canonical snapshot persistence and final transactional activation authority.
- `deploy --allow-insecure-loopback` explicitly permits HTTP only for loopback development. HTTPS/capability and redirect refusal remain shared typed-client rules. Schema-less legacy uploads do not newly negotiate support or preflight. Errors retain the safe typed-client status-only remote-error convention; preflight failures cannot echo server bodies.

## Focused verification executed

After implementation and permanent tests were finished:

```sh
gofmt -w cmd/hostedat/main.go cmd/hostedat/deploy_test.go internal/client/deploy.go internal/client/deploy_schema_test.go internal/client/deploy_test.go internal/client/client_test.go internal/client/env_test.go
go test -race ./cmd/hostedat ./internal/client ./internal/envschema
go build -o /tmp/hostedat-task14 ./cmd/hostedat
go build -o /tmp/hostedat-task14-server ./cmd/server
```

All passed: command package 1.085s, client 1.256s, envschema cached; both production binary builds exit 0. No product changes after verification; remaining changes were docs/evidence only.

Permanent consumer regressions cover invocation-root discovery while uploading dist, JSON/YAML/explicit selection, explicit empty and schema-less cases, both-default/missing-source failures before auth/network, canonical request bodies, capability → preflight → upload ordering, exclusion of local dotenv/shell markers, selected-source/hard-link/symlink handling, parsed-snapshot stability when source bytes change, separately supplied case-insensitive/nested canonical conflicts, direct-ZIP preflight and fail-closed 400/false-valid/malformed responses without upload or body disclosure. Prior deployment/client/schema tests continue to pass. These HTTP test servers are contract tests, not engine proof.

## Actual built CLI / production server / real QuickJS smoke

Tool-session Python orchestration created disposable operator-owned 0600 AES key material, config, SQLite/storage, invocation projects and capture/log buffers under `/tmp/hostedat-task14-*`. Ran the actual server offline migration, then the production server with default real QuickJS on ephemeral loopback HTTP and domain `secret.test`. CLI used explicit loopback opt-in for manifest operations. Registered one disposable account and created separate main and legacy sites through real APIs.

A recording HTTP forwarder passed requests/responses to the actual production server without simulating validation, mutations, deployment or execution. For the old-server compatibility case only, it removed capabilities from the real version response; all other responses were unchanged. This is absent-capability compatibility evidence, not a historical server binary run.

Observed results:

1. With required TASK14_TOKEN absent, native JSON-backed dist deploy failed in remote preflight. Captures proved no multipart deploy request; persisted deployment count remained zero.
2. Explicit native stdin put provisioned a unique disposable secret. JSON-backed dist deploy then succeeded; actual site fetch executed real QuickJS and returned `200 binding-ok` after checking the binding is a string with the expected length. Worker source/response did not contain the secret marker.
3. YAML-only default discovery deployed the same dist successfully. When both defaults existed, implicit discovery failed with zero requests. Explicit JSON and explicit YAML selections each deployed successfully.
4. A fifth manifest deploy selected `dist/requirements.yml` explicitly. The original source file was absent from the uploaded ZIP, replaced by the canonical artifact-root declaration.
5. Decompressed every captured manifest ZIP: all five contained exactly `index.html`, `_worker.js` and `_hostedat.env.json`. Every manifest was byte-identical canonical JSON:

   ```json
   {"TASK14_TOKEN":{"type":"string","required":true}}
   ```

   No dotenv component, symlink, source JSON/YAML, local marker or production secret marker appeared in the decompressed entries. Associated local `.env` deliberately contained malformed marker-bearing syntax and the declared shell variable held a local marker in command regression tests; deployment did not load them. Mixed-case `.ENV.PRODUCTION` and `.EnV.hidden/hidden.txt` plus a symlink to root dotenv were excluded in the actual smoke.
6. A separate equal canonical manifest inside dist failed before upload, rather than choosing/merging it. Explicit missing `.json` and unsupported `.yaml` failed with zero requests. Manifest deploy over HTTP without explicit opt-in sent neither validation nor upload.
7. With capabilities removed from the forwarded version response, built CLI returned upgrade guidance and sent neither validation nor upload. No legacy upload fallback occurred.
8. In an independent project with no schema, native deploy to the legacy site succeeded over the existing HTTP path, even with capabilities absent. Captures contained only existing site resolution and upload, no version/preflight calls. ZIP contained only safe index/worker, excluding mixed-case `.ENV`. Real QuickJS fetch returned `200 legacy-ok`.
9. Inspected actual persisted snapshots: all five main-site deployment rows held identical canonical declarations; the legacy snapshot was NULL and its environment remained empty. Six preflight POSTs were recorded (one missing-secret failure, five successful manifest validations), all declaration-only. Main-site final real invocation still returned `200 binding-ok`.
10. Local marker was absent from every captured request; production marker appeared only in its explicit secret put request, never declaration preflight/upload. Both markers were absent from all decompressed upload files, actual extracted storage files (18 inspected), management list response, CLI output/errors and production logs. No HOME/config/target file was created.
11. SIGINT stopped the actual server with exit 0. Shut down forwarder and removed disposable keys/database/storage/inputs/captures/logs and both binaries. Orchestration existed only in the tool session; no throwaway repository scripts/scaffolds remain.

Two fixture-only diagnostic corrections were needed, with no code changes or skipped checks: initial server config used a too-short JWT secret and was corrected before migration/start; an optional snapshot inspection first queried nonexistent `env_schema`, then inspected SQLite table metadata and used the actual `env_declaration` column. All deployment/ZIP/engine assertions completed successfully.

## Documentation and remaining risks

After proof, updated existing public CLI reference, docs-site CLI deployment section and CHANGELOG; removed stale packaging-not-integrated wording. README unchanged. Only task 14/original task 17 checkboxes were updated.

No task-14 blocker. Preflight is diagnostic and cannot eliminate races; established server activation transactions remain the final authority. Filename exclusions cannot discover secrets renamed/embedded in bundles; declarations (including enum literals) are metadata, not a value-export mechanism. Unselected ordinary declaration-named files are not globally excluded; only the selected source is canonicalized/excluded, as specified. Authorized workers can disclose their own bindings, and existing in-flight snapshots retain their strings.

No UI changed, so browser smoke was not needed or claimed. V8, full acceptance/full suite, frontend/docs release builds and final release-wide documentation remain task 15. This task exercised actual default QuickJS, not a mocked engine.
