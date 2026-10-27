# Task 12 — Native secret commands

Status: COMPLETE on `feat-secrets-sync`. Commit title: `Add native secret commands with bounded hidden input and atomic sync`; exact commit hash returned to the parent. Only task 12/original task 15 checked. No branch, README edit, push, env check command or deploy-schema integration.

## Implementation and API guidance

- Registered `hostedat secret put <site> <name>`, `sync <site> <file>`, `list <site> [--json]`, and `delete <site> <name> [--yes]`. All reuse `newClient` credential resolution and `ResolveSite`, including its existing ambiguity diagnostics. No linked project, saved site, new credential store, source-path persistence or application launch/build.
- Group flag `--allow-insecure-loopback` explicitly sets `Client.AllowInsecureLoopback`; it is never inferred from HTTP. All commands verify secure transport and `secrets-env-v1` before resolving the site, so credentials are not sent to insecure remote URLs. Mutations retain typed-client capability negotiation and redirect refusal. Read commands intentionally also require capability rather than accepting legacy secret-disclosing servers.
- Put discovers a declaration once, checks the name and selected declaration, resolves credentials/server/site before reading input, and emits a quoted server/site name/ID target on Cobra stderr. Cobra stdin/stdout/stderr are used throughout new commands. Non-terminal input requires `--stdin`; there is no value flag or positional value.
- Stdin reads at most the shared 1 MiB value bound plus delimiter/overflow detection, removes exactly one final LF or CRLF, and preserves all other whitespace and multiline content. UTF-8 and declaration constraints are checked before a write. Hidden terminal input uses `golang.org/x/term v0.39.0` raw mode with bounded reads, Enter, UTF-8-aware backspace and Ctrl-C/Ctrl-D cancellation; terminal state is restored on every return. Standard ReadPassword was deliberately not used because it buffers unbounded input. Multiline credentials use explicit stdin.
- Put/sync reuse `envschema.Discover`, `Project.ReadSyncSource`, `Schema.ValidateValue`, static name/runtime reservations and the existing typed client. Nil candidate means absent; explicit empty schema is retained. Candidates constrain supplied names only and never activate deployment requirements; active server declarations and database-aware collisions remain authoritative.
- Sync reads only a named regular dotenv source, never implicit local dotenv or shell overlays. The existing helper rejects the associated root `.env` and equivalent symlink/hard-link aliases, bounds the complete source and rejects duplicates/parser errors safely. The whole source is checked before **any** server request. Entry/value/aggregate bounds use the exact exported `environment` server constants. Sorted entries go through one `BulkSetEnvVars` request; no sequential fallback, replacement or pruning. Every write explicitly sets `Secret:true`.
- List filters plaintext bindings, sorts secrets by name and constructs a separate metadata-only DTO (`id,site_id,name,secret`). JSON never serializes client values, even from a nonconforming response; empty lists are `[]`. Table includes name, ID and true secret flag.
- Delete lists within the resolved site, matches a name, refuses duplicate metadata/plaintext, asks the existing case-insensitive `y` confirmation through Cobra streams unless `--yes`, then deletes the exact site-scoped ID. Active required-secret deletion remains server-enforced.
- `ResolveSite` now wraps list-request failures with the existing body-independent `safeClientError`, so resolving a target cannot print an arbitrary server body or transport diagnostic. Its matching/ambiguity behavior is unchanged.
- `secretProject` and `secretTarget` are command-local helpers available to later CLI integrations. Local-only env check must not call `secretTarget` or read a sync source; remote validation must use declarations-only `ValidateEnvVars` from task 11. No exported client/schema interfaces changed in this task.

## Focused verification executed

Implementation and permanent consumer tests were completed before formatting/build/tests:

```sh
gofmt -w cmd/hostedat/secret.go cmd/hostedat/secret_test.go cmd/hostedat/main.go internal/client/sites.go
go test -race ./cmd/hostedat ./internal/client ./internal/envschema
go build -o /tmp/hostedat-task12 ./cmd/hostedat
go build -o /tmp/hostedat-task12-server ./cmd/server
```

All passed: CLI 1.061s, client 1.244s, envschema cached pass; both builds exit 0. Moving the terminal module from indirect to direct requirements afterward only corrected dependency classification; no version or product code changed after these checks.

Permanent command regressions cover exact LF/CRLF/whitespace/multiline removal, empty and exact-bound input, overflow, non-TTY refusal, existing auth resolution, sorted secret-only metadata, malicious secret response-value removal, a single schema-bearing bulk request, invalid complete-source rejection before network, local dotenv rejection, plaintext-delete refusal, confirmation refusal/acceptance, `--yes`, missing site/auth and safe output. Existing full client package tests additionally retain capability/transport/redirect, site ambiguity, authentication and deploy contracts. No mocked engine is presented as runtime proof.

## Actual built CLI / production server / real PTY and QuickJS smoke

A tool-driven Python consumer used only standard library HTTP/subprocess/PTY/SQLite facilities, random disposable credentials and operator-generated 0600 key material under `/tmp/hostedat-task12-*`. It built the production binaries above, then ran:

```sh
/tmp/hostedat-task12-server --config TEMP/config.yaml --secrets-maintenance migrate --writers-stopped
/tmp/hostedat-task12-server --config TEMP/config.yaml
```

Migration succeeded. Server used ephemeral literal-loopback HTTP, explicit CLI `--allow-insecure-loopback`, domain `secret.test`, SQLite and the real default QuickJS engine. No UI was changed or needed for this task.

Observed steps/results:

1. Registered a random disposable account and created a site via actual API. Built CLI put through a pipe created TOKEN. List JSON contained only metadata and no value field.
2. Opened a real PTY for built CLI interactive put of HIDDEN. Waited for the hidden prompt and verified terminal ECHO was disabled before typing a unique secret marker. Captured all PTY output: safe target and success appeared; the typed marker did not. A second real PTY run sent Ctrl-C, exited 1, and restored **every** original terminal attribute.
3. Server-side enum candidate validation of decrypted stored values proved exact leading spaces, trailing space/tab, removal of exactly one CRLF and the hidden-input value without revealing them. A later stdin replacement contained leading space/tab and a physical multiline value with two terminal LFs; remote enum validation proved exactly one LF remained.
4. Built CLI rejected missing auth, unknown site, implicit piped non-TTY input, HTTP without explicit opt-in and unsupported `--value`, without disclosing the unique input marker. Actual duplicate display-name sites produced existing ambiguity guidance; the ID remained unambiguous. Site slug, display name and ID references all worked.
5. Created an unrelated secret and a plaintext binding. YAML-backed native sync wrote TOKEN and numeric COUNT in one successful merge. Previously existing IDs remained stable, omitted HIDDEN/UNRELATED remained intact, and plaintext stayed present remotely but never in native secret list/table/JSON. A malformed numeric batch failed without changing TOKEN/COUNT. Local `.env` and an external symlink alias to it were refused.
6. Installed a real SQLite trigger that aborts insertion of FAIL after a preceding COUNT update. Native sync failed safely. Remote decrypted enum validation proved COUNT remained `7`; database lookup proved no FAIL row. No per-name fallback or partial commit occurred. The trigger used a fixed operator diagnostic, not a secret value.
7. Uploaded a real declaration-bearing ZIP through production API with canonical TOKEN/string and COUNT/number, a marker-free worker and safe index. Real QuickJS site fetch returned `200 binding-ok` after checking string type, secret length and COUNT. A native TOKEN update then made the next real invocation return `200 binding-changed` without redeploy or disclosing any value.
8. Explicit JSON candidate permitting COUNT/string could not bypass active COUNT/number: put failed and remote enum validation still proved `7`. Required TOKEN delete failed. Plaintext delete was refused. HIDDEN delete with `n` aborted, `Y` succeeded, and `--yes` removed UNRELATED. Final sorted secret metadata contained COUNT/TOKEN, with original TOKEN ID.
9. Actual management list, all captured CLI outputs/errors/PTY bytes and production logs contained no unique secret marker. Both remaining live secret rows had empty plaintext, version 1, correct key ID, nonce and ciphertext; ciphertext omitted the marker. No CLI config/target file was created. The worker ZIP did not contain the marker; worker responses contained only fixed outcome labels.
10. Stopped the server with SIGINT, exit 0. Closed SQLite and removed disposable database, keys, storage, sources, captures/logs and both binaries. The orchestration existed only in the tool session; no throwaway script/scaffolding was created in the repository.

All smoke assertions passed on their first run; no product corrections or retries were required after implementation.

## Documentation and remaining risks

After proof, updated existing public CLI reference, docs-site CLI page and CHANGELOG; corrected the relevant CLI reference's outdated site-precedence description to match established ambiguity handling. README unchanged. Recorded task-12 evidence and checked only its completed boxes.

No unresolved task-12 blocker. Full suite, frontend/docs release builds, V8 coverage, env check commands and CLI schema-source packaging remain tasks 13–15. No browser/UI verification was run here because no UI changed. Environment client status-only errors intentionally omit remote names/reasons; local declaration errors remain detailed and value-free. Loopback HTTP is explicitly insecure development transport, never production permission. Authorized workers can disclose their values, and in-flight invocations retain old strings; neither at-rest encryption nor CLI redaction changes those limits. Operator-authored SQLite diagnostic text can appear in internal logs, as already documented in task 11; this smoke's actual submitted marker remained absent.
