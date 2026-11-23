# google-contacts-mcp

## Purpose

- Remote, multi-tenant MCP server exposing user's Google Contacts to Claude and ChatGPT.
- OAuth 2.1 authorization server toward MCP clients (ory/fosite); confidential OAuth client toward Google (People API v1).
- One Go binary, one SQLite file, exactly one instance. Contact data never touches disk.
- Module `github.com/samber/google-contacts-mcp`, binary `google-contacts-mcp`, `go 1.26` in `go.mod`, `CGO_ENABLED=0`.

## Package map

- `cmd/google-contacts-mcp`: subcommands `serve`, `genkey`, `rekey`, `disconnect`, `version`; wiring.
- `internal/config`: flags plus `GCMCP_*` env, per-command required values.
- `internal/contacts`: contact model, People API mapping, enrich/merge rules, typed errors.
- `internal/crypto`: rotatable AES-256-GCM keyring (`keyid:base64`).
- `internal/google`: Google OAuth exchange, per-user token source, People API client.
- `internal/jobs`: per-minute flush of batched `last_seen_at` (`TouchUser` only writes memory; `Store.Close` flushes too), hourly expired-row cleanup, daily 180-day inactive-user purge (guarded against reconnects), purge of clients unused 180 days or never used after 24 h.
- `internal/limits`: per-user read/write token buckets, per-user write lock.
- `internal/oauth`: authorization server: metadata, CIMD, DCR, consent, Google callback, token, revoke.
- `internal/obs`: Prometheus metrics, per-call tool logging.
- `internal/store`: SQLite store, embedded migrations, fosite storage, users and encrypted Google tokens.
- `internal/tools`: MCP server and ten contact tools.
- `internal/web`: HTTP mux (MCP, OAuth), `GET /` redirect to `--site-url`, `GET /.well-known/openai-apps-challenge` (only with `--openai-apps-challenge`), metrics basic auth. Renders no page.
- `plugin/openai/`: ChatGPT/Codex plugin package (`plugin.json`, `mcp.json`, icon); `make plugin-openai` zips it into `dist/`. Submission steps for it and for the Claude connector: `docs/directory-submission.md`.
- `site/`: Astro website (landing page, privacy policy, terms of service) for the hosted instance; own gates and rules in `site/CLAUDE.md`.

## Gates

Run all four before every commit. `Makefile` has no `gofmt` or `vet` target; run those directly.

```sh
gofmt -l .            # must print nothing
go vet ./...
make lint             # golangci-lint run
make test             # go test -race -v ./...
```

Run `make plugin-openai` whenever anything in `plugin/openai/` changes, or a tool name, title or description changes. Bump `version` in `plugin/openai/plugin.json` first: OpenAI rejects a changed package with an old version. Then upload `dist/google-contacts-openai-plugin.zip` in the OpenAI portal.

Other `make` targets: `build`, `coverage` (writes `cover.out`, `cover.html`), `bench`, `watch-test`, `tools`, `lint-fix`, `audit`, `outdated`, `weight`.

## CI and automation

- `.github/workflows/test.yml`: push to `main` and PRs; matrix Go `go.mod` + `stable`; runs `make build`, `make test`, `make coverage`, uploads to Codecov on `stable`.
- `.github/workflows/lint.yml`: push to `main`, PRs, weekly cron, manual; `golangci-lint-action` with `.golangci.yml`. Version not pinned.
- `.github/workflows/security.yml`: push to `main`, PRs, weekly cron, manual; Bearer, CodeQL, two Trivy jobs (`.trivyignore` referenced, file missing). `govulncheck` job commented out.
- `.github/workflows/release.yml`: on a `v*` tag, reruns gofmt, vet, golangci-lint, `go test -race`, then cross-compiles `tar.gz` archives (linux and darwin, amd64 and arm64) with `go build`, and `gh release create` publishes them with `checksums.txt` and generated notes. No Docker image is published.
- `.github/workflows/dependabot-automerge.yaml`: auto-merge (squash) of Dependabot `github_actions` PRs only.

## Domain jargon

- **CIMD**: Client ID Metadata Document. `client_id` is `https://` URL; server fetches JSON (SSRF guard, 5 s, 64 KiB, cached up to 24 h, served stale max 7 days, dropped on 404/410/400).
- **DCR**: Dynamic Client Registration (RFC 7591), `POST /oauth/register`, public clients only, `--enable-dcr`.
- **PKCE**: Proof Key for Code Exchange. S256 only, toward clients and toward Google.
- **RFC 9207 `iss`**: issuer parameter added to every authorization response.
- **RFC 8707 `resource`**: must equal `<base-url>/mcp`; tokens get that audience.
- **RFC 9728**: protected-resource metadata at `/.well-known/oauth-protected-resource` and `/.well-known/oauth-protected-resource/mcp`.
- **People API `resourceName`**: contact id (`people/c123`); groups are `contactGroups/abc`. Tools accept only these two shapes.
- **CONTACT source etag**: etag of person's `CONTACT` source; updates must send current one, stale etag = `failedPrecondition`.
- **Testing mode**: unverified Google app; max 100 test users, Google refresh tokens expire 7 days after consent.
- **Network volume**: the SQLite file may sit on a mounted network filesystem, so journal mode is DELETE, not WAL. No replication or backup ships with the project.

## Conventions

- Never log contact data, tool arguments or results above `debug`. Never log tokens, codes, secrets or Google response bodies.
- No magic numbers or strings inline: named constants, each with one-line "why" comment.
- Never ignore error. Structured `slog` logging.
- User deletion runs explicit `deleteUserChildren` list (`internal/store/users.go`); add every new per-user table to it.
- Every tool needs a `title` and explicit `readOnlyHint`, `destructiveHint`, `openWorldHint`: Claude's and OpenAI's directories reject tools without them (`TestToolAnnotations`).
- Flags must stay in sync with the configuration table in `docs/self-hosting.md`.
- Conventional Commits.

## References

- Design spec: `docs/superpowers/specs/2026-11-19-google-contacts-mcp-design.md`
- Roadmap: GitHub issues labelled `enhancement`
