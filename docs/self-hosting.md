# Self-hosting

Run your own `trakt-mcp` instance: one Go binary, one SQLite file, exactly one process. For what the server does and how to use it, see the [README](../README.md).

## Quick start (local)

You need Go 1.26 or newer and a Trakt API application (see [Trakt application setup](#trakt-application-setup); for local use, add `http://localhost:8080/oauth/trakt/callback` as a redirect URI).

```sh
go install github.com/samber/trakt-mcp/cmd/trakt-mcp@latest

# Generate an encryption key for the Trakt tokens stored in the database.
export ENCRYPTION_KEYS="$(trakt-mcp genkey)"
export TRAKT_CLIENT_SECRET="<your Trakt client secret>"

trakt-mcp serve \
  --base-url http://localhost:8080 \
  --site-url http://localhost:4321 \
  --trakt-client-id "<your Trakt client id>"
```

The server listens on `127.0.0.1:8080`, stores its database in `./data/trakt-mcp.db`, and serves the MCP endpoint at `http://localhost:8080/mcp`. The server renders no web page: `GET /` redirects to `--site-url`. To check it is running, open `http://localhost:8080/.well-known/oauth-authorization-server`, which returns JSON.

Plain `http` is accepted only for `localhost` and `127.0.0.1`. Claude and ChatGPT connect from their own cloud, so they cannot reach a server on your laptop: to try them, expose the server through an HTTPS tunnel and use the tunnel URL as `--base-url`, or deploy it on a server with a public HTTPS address.

No prebuilt binary or Docker image is published today: build with `go install` or `make build` (`CGO_ENABLED=0` works).

## Commands

| Command                                       | Purpose                                                                   |
| --------------------------------------------- | ------------------------------------------------------------------------- |
| `serve` (default)                             | Runs the server.                                                          |
| `genkey`                                      | Prints a new encryption key entry (`keyid:base64`) for `ENCRYPTION_KEYS`. |
| `rekey`                                       | Re-encrypts every stored Trakt token with the active (first) key.         |
| `disconnect <trakt slug or numeric Trakt id>` | Revokes the user's grant at Trakt and deletes every row about them.       |
| `version`                                     | Prints the version.                                                       |

Flags come before positional arguments, for example `trakt-mcp disconnect --db-path /data/trakt-mcp.db 1234567`. Prefer the numeric Trakt id: a slug can change, and `disconnect` prints the Trakt id, slug and internal user id it matched.

## Deploys and shutdown

- Stop the old instance before you start the new one. Token refreshes are coordinated inside one process only: two processes on one database can race on a user's single-use Trakt refresh token, and the loser deletes that user, who must reconnect. Most PaaS hosts overlap old and new instances during a deploy by default; switch that off (stop-then-start, or a single-instance deploy strategy).
- Give the process a kill grace period of at least 50 seconds after `SIGTERM`. On shutdown the server drains requests for up to 15 seconds, then waits up to 35 seconds for token refreshes still running, so a rotated Trakt token is saved before the database closes. A shorter grace (Fly.io defaults to 5 s, Cloud Run to 10 s) can kill a refresh after Trakt has rotated the grant; that user must then reconnect.

## Configuration

Every flag can also be set with an environment variable of the same name, uppercased, with dashes replaced by underscores (`--db-path` becomes `DB_PATH`). There is no prefix. When both are set, the flag wins. The two secrets, `TRAKT_CLIENT_SECRET` and `ENCRYPTION_KEYS`, can only be set in the environment, so they never appear in process listings.

| Flag                      | Environment variable    | Default                                                 | Purpose                                                                                                                                                                                  |
| ------------------------- | ----------------------- | ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--base-url`              | `BASE_URL`              | required                                                | Public HTTPS URL of the server. Also the OAuth issuer. `http` is accepted only for `localhost` and `127.0.0.1`.                                                                          |
| `--site-url`              | `SITE_URL`              | required for `serve`                                    | Website URL. `GET /` redirects to it with a 302. Must be `https`, or `http` on `localhost`.                                                                                              |
| `--listen`                | `LISTEN`                | `127.0.0.1:8080`, or `0.0.0.0:$PORT` when `PORT` is set | Listen address.                                                                                                                                                                          |
| `--db-path`               | `DB_PATH`               | `./data/trakt-mcp.db`                                   | SQLite file. One server instance only; `rekey` and `disconnect` may run beside it. Rollback journal: a network volume works.                                                             |
| `--trakt-client-id`       | `TRAKT_CLIENT_ID`       | required                                                | Trakt application client id, also sent as the `trakt-api-key` header.                                                                                                                    |
| (environment only)        | `TRAKT_CLIENT_SECRET`   | required                                                | Trakt application client secret.                                                                                                                                                         |
| (environment only)        | `ENCRYPTION_KEYS`       | required                                                | Comma-separated `keyid:base64` keys from `genkey`. The first one encrypts new data; the others still decrypt.                                                                            |
| `--enable-dcr`            | `ENABLE_DCR`            | `true`                                                  | Keep OAuth Dynamic Client Registration for clients that do not support Client ID Metadata Documents.                                                                                     |
| `--log-level`             | `LOG_LEVEL`             | `info`                                                  | `debug`, `info`, `warn` or `error`. `debug` writes tool arguments and results, which contain personal data, to the logs.                                                                 |
| `--metrics`               | `METRICS`               | `false`                                                 | Enable the Prometheus endpoint.                                                                                                                                                          |
| `--metrics-path`          | `METRICS_PATH`          | `/metrics`                                              | Metrics endpoint path.                                                                                                                                                                   |
| `--metrics-listen`        | `METRICS_LISTEN`        | main listener                                           | Optional separate address for metrics, e.g. `127.0.0.1:9090`.                                                                                                                            |
| `--metrics-user`          | `METRICS_USER`          | none                                                    | Basic-auth user, required with `--metrics`.                                                                                                                                              |
| `--metrics-password`      | `METRICS_PASSWORD`      | none                                                    | Basic-auth password, 16 characters or more. Required with `--metrics` unless `--metrics-password-file` is set.                                                                           |
| `--metrics-password-file` | `METRICS_PASSWORD_FILE` | none                                                    | File holding the basic-auth password. Safer than passing it on the command line. Cannot be combined with `--metrics-password`.                                                           |
| `--rate-limit-reads`      | `RATE_LIMIT_READS`      | `120`                                                   | Read tool calls per user per minute.                                                                                                                                                     |
| `--rate-limit-writes`     | `RATE_LIMIT_WRITES`     | `30`                                                    | Write tool calls per user per minute.                                                                                                                                                    |
| `--rate-limit-global`     | `RATE_LIMIT_GLOBAL`     | `1200`                                                  | Trakt requests per minute across all users, so the instance stays inside the application's Trakt budget.                                                                                 |
| `--openai-apps-challenge` | `OPENAI_APPS_CHALLENGE` | none                                                    | OpenAI domain-verification token, served as plain text at `/.well-known/openai-apps-challenge`. Without it, that path answers 404. See [directory submissions](directory-submission.md). |

`serve` requires `--base-url`, `--site-url`, `--db-path`, `--trakt-client-id`, `TRAKT_CLIENT_SECRET` and `ENCRYPTION_KEYS`. `rekey` needs only `--db-path` and `ENCRYPTION_KEYS`. `disconnect` also needs the Trakt client id and secret, to revoke the grant at Trakt.

**Name collisions.** The unprefixed names can clash with variables your host or shell already sets. Check these before you deploy: `BASE_URL`, `SITE_URL`, `LISTEN`, `DB_PATH`, `TRAKT_CLIENT_ID`, `TRAKT_CLIENT_SECRET`, `ENCRYPTION_KEYS`, `ENABLE_DCR`, `LOG_LEVEL`, `METRICS`, `METRICS_PATH`, `METRICS_LISTEN`, `METRICS_USER`, `METRICS_PASSWORD`, `METRICS_PASSWORD_FILE`, `RATE_LIMIT_READS`, `RATE_LIMIT_WRITES`, `RATE_LIMIT_GLOBAL`, `OPENAI_APPS_CHALLENGE`, plus `PORT`.

## Trakt application setup

1. Sign in to Trakt and open [trakt.tv/oauth/applications](https://trakt.tv/oauth/applications). Create a new application.
2. Fill the name and description. Add the redirect URI `https://<host>/oauth/trakt/callback`, where `<host>` is the host of `--base-url`.
3. Save, then copy the client id into `--trakt-client-id` and the client secret into `TRAKT_CLIENT_SECRET`.

Trakt sends no granular scopes: a connected user grants full access to their account. Read Trakt's API use policy before you open an instance to the public.

**Rotating the Trakt client secret.** Regenerate the secret on the application page, set it in `TRAKT_CLIENT_SECRET`, and restart. Trakt's behavior with two live secrets is not documented; verify before rotating. Assume refreshes fail between the two steps, so do it during a quiet period.

**Rotating the encryption key.**

1. Run `trakt-mcp genkey` and put the new entry first in `ENCRYPTION_KEYS`, keeping the old one after it: `ENCRYPTION_KEYS="<new>,<old>"`.
2. Restart the server.
3. Run `trakt-mcp rekey` against the live database. It re-encrypts every Trakt token with the new key.
4. Keep the old key listed for 180 days. The tokens the server issues to assistants are signed with a secret derived from the keys, so removing the old key early forces every user to reconnect.

## Website

`GET /` redirects to `--site-url`. The `site/` directory holds the Astro site of the hosted instance (landing page, privacy policy, terms), which you can adapt. Publish your own privacy policy: it must match what the server stores (see the README).

Once the server runs, connect your assistant as described in [Connect Claude and ChatGPT](../README.md#1-connect-claude-and-chatgpt).
