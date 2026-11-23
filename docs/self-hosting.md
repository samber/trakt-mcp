# Self-hosting

Run your own `google-contacts-mcp` instance: one Go binary, one SQLite file, exactly one process. For what the server does and how to use it, see the [README](../README.md).

## Quick start (local)

You need Go 1.26 or newer and a Google OAuth client (see [Google Cloud setup](#google-cloud-setup); for local use, add `http://localhost:8080/oauth/google/callback` as an authorized redirect URI).

```sh
go install github.com/samber/google-contacts-mcp/cmd/google-contacts-mcp@latest

# Generate an encryption key for the Google tokens stored in the database.
export GCMCP_ENCRYPTION_KEYS="$(google-contacts-mcp genkey)"
export GCMCP_GOOGLE_CLIENT_SECRET="<your Google client secret>"

google-contacts-mcp serve \
  --base-url http://localhost:8080 \
  --google-client-id "<your Google client id>.apps.googleusercontent.com"
```

The server listens on `127.0.0.1:8080`, stores its database in `./data/google-contacts-mcp.db`, and serves the MCP endpoint at `http://localhost:8080/mcp`. The server renders no web page. To check it is running, open `http://localhost:8080/.well-known/oauth-authorization-server`, which returns JSON.

Plain `http` is accepted only for `localhost` and `127.0.0.1`. Claude and ChatGPT connect from their own cloud, so they cannot reach a server on your laptop: to try them, expose the server through an HTTPS tunnel and use the tunnel URL as `--base-url`, or deploy it on a server with a public HTTPS address.

Prebuilt `tar.gz` archives for linux and darwin (amd64 and arm64) are attached to each [GitHub release](https://github.com/samber/google-contacts-mcp/releases), with a `checksums.txt` file. No Docker image is published.

## Commands

| Command                                   | Purpose                                                                         |
| ----------------------------------------- | ------------------------------------------------------------------------------- |
| `serve` (default)                         | Runs the server.                                                                |
| `genkey`                                  | Prints a new encryption key entry (`keyid:base64`) for `GCMCP_ENCRYPTION_KEYS`. |
| `rekey`                                   | Re-encrypts every stored Google token with the active (first) key.              |
| `disconnect <email or Google account id>` | Revokes the user's grant at Google and deletes every row about them.            |
| `version`                                 | Prints the version.                                                             |

Flags come before positional arguments, for example `google-contacts-mcp disconnect --db-path /data/db.sqlite alice@example.com`.

## Configuration

Every flag can also be set with an environment variable: `GCMCP_` followed by the flag name in upper snake case (`--db-path` becomes `GCMCP_DB_PATH`). When both are set, the flag wins. The two secrets can only be set in the environment, so they never appear in process listings.

| Flag                      | Environment variable          | Default                                                 | Purpose                                                                                                                                 |
| ------------------------- | ----------------------------- | ------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| `--base-url`              | `GCMCP_BASE_URL`              | required                                                | Public HTTPS URL of the server. Also the OAuth issuer. `http` is accepted only for `localhost` and `127.0.0.1`.                         |
| `--listen`                | `GCMCP_LISTEN`                | `127.0.0.1:8080`, or `0.0.0.0:$PORT` when `PORT` is set | Listen address.                                                                                                                         |
| `--db-path`               | `GCMCP_DB_PATH`               | `./data/google-contacts-mcp.db`                         | SQLite file. One server instance only; `rekey` and `disconnect` may run beside it. Rollback journal: a network volume works.            |
| `--google-client-id`      | `GCMCP_GOOGLE_CLIENT_ID`      | required                                                | Google OAuth client id.                                                                                                                 |
| (environment only)        | `GCMCP_GOOGLE_CLIENT_SECRET`  | required                                                | Google OAuth client secret.                                                                                                             |
| (environment only)        | `GCMCP_ENCRYPTION_KEYS`       | required                                                | Comma-separated `keyid:base64` keys from `genkey`. The first one encrypts new data; the others still decrypt.                           |
| `--allowed-domains`       | `GCMCP_ALLOWED_DOMAINS`       | empty (every account)                                   | Comma-separated domains allowed to connect, e.g. `acme.com,gmail.com`. See [Restricting who can connect](#restricting-who-can-connect). |
| `--enable-dcr`            | `GCMCP_ENABLE_DCR`            | `true`                                                  | Keep OAuth Dynamic Client Registration for clients that do not support Client ID Metadata Documents.                                    |
| `--log-level`             | `GCMCP_LOG_LEVEL`             | `info`                                                  | `debug`, `info`, `warn` or `error`. `debug` writes tool arguments and results, which contain personal data, to the logs.                |
| `--metrics`               | `GCMCP_METRICS`               | `false`                                                 | Enable the Prometheus endpoint.                                                                                                         |
| `--metrics-path`          | `GCMCP_METRICS_PATH`          | `/metrics`                                              | Metrics endpoint path.                                                                                                                  |
| `--metrics-listen`        | `GCMCP_METRICS_LISTEN`        | main listener                                           | Optional separate address for metrics, e.g. `127.0.0.1:9090`.                                                                           |
| `--metrics-user`          | `GCMCP_METRICS_USER`          | none                                                    | Basic-auth user, required with `--metrics`.                                                                                             |
| `--metrics-password`      | `GCMCP_METRICS_PASSWORD`      | none                                                    | Basic-auth password, 16 characters or more. Required with `--metrics` unless `--metrics-password-file` is set.                          |
| `--metrics-password-file` | `GCMCP_METRICS_PASSWORD_FILE` | none                                                    | File holding the basic-auth password. Safer than passing it on the command line. Cannot be combined with `--metrics-password`.          |
| `--rate-limit-reads`      | `GCMCP_RATE_LIMIT_READS`      | `120`                                                   | Read tool calls per user per minute.                                                                                                    |
| `--rate-limit-writes`     | `GCMCP_RATE_LIMIT_WRITES`     | `30`                                                    | Write tool calls per user per minute.                                                                                                   |
| `--site-url`              | `GCMCP_SITE_URL`              | none                                                    | Website URL. `GET /` redirects to it with a 302. Without it, `/` answers 404. Must be `https`, or `http` on `localhost`.                |
| `--openai-apps-challenge` | `GCMCP_OPENAI_APPS_CHALLENGE` | none                                                    | OpenAI domain-verification token, served as plain text at `/.well-known/openai-apps-challenge`. Without it, that path answers 404. See [directory submissions](directory-submission.md). |

`rekey` needs only `--db-path` and `GCMCP_ENCRYPTION_KEYS`. `disconnect` also needs the Google client id and secret, to revoke the grant at Google.

### Restricting who can connect

With `--allowed-domains` empty, any Google account can connect. When it is set, an account passes for a listed domain `D` when either:

- Google reports the account as part of Google Workspace domain `D` (the `hd` claim in Google's ID token), or
- the account has no Workspace domain, its e-mail address ends in `@D`, and `D` is `gmail.com` or `googlemail.com`.

Listing a non-Google domain therefore admits only Workspace accounts of that domain. A personal Google account created with an `@acme.com` address is refused even when `acme.com` is listed, because anyone can create such an account without owning the domain. Refused users see an explicit refusal page.

## Google Cloud setup

You need a Google Cloud project with an OAuth client. This takes about 15 minutes.

1. In the [Google Cloud console](https://console.cloud.google.com), create a project, for example `google-contacts-mcp`.
2. Open **APIs & Services > Library**, search for **Google People API** and enable it.
3. Open **Google Auth Platform** (formerly the OAuth consent screen) and configure it:
   - **Branding:** app name, support e-mail, and the links to your home page and privacy policy. The server serves neither: host them on your own website, on a domain you add to the authorized domains. The `site/` directory holds the Astro site used by the hosted instance, which you can adapt.
   - **Audience:** choose **External**, so any Google account can connect, or **Internal** to limit it to your Workspace organization.
   - **Data access:** add exactly three scopes: `openid`, `.../auth/userinfo.email` (shown as `email`) and `https://www.googleapis.com/auth/contacts`.
4. Open **Clients**, create an OAuth client of type **Web application**, and add the authorized redirect URI `https://<host>/oauth/google/callback`. Copy the client id and secret into `--google-client-id` and `GCMCP_GOOGLE_CLIENT_SECRET`.

**Testing mode versus a verified app.** A new External app starts in Testing mode. Only the test users you list under **Audience** can connect, at most 100 of them, and Google expires their grant 7 days after consent: each user must reconnect every week. To lift both limits, publish the app and submit it for verification. The contacts scope is sensitive, not restricted, so verification needs no security assessment. Google checks that your home page and privacy policy live on your verified domain; the server serves both pages for you.

Users not on the test list see Google's own refusal, then a generic "Google sign-in failed" page from this server.

**Restricting accounts.** To accept only your company, either choose the Internal audience in Google, or set `--allowed-domains` (see [Restricting who can connect](#restricting-who-can-connect)).

**Rotating the Google client secret.** In **Clients**, add a second secret to the client. Set it in `GCMCP_GOOGLE_CLIENT_SECRET`, restart the server, check that a new connection works, then disable and delete the old secret.

**Rotating the encryption key.**

1. Run `google-contacts-mcp genkey` and put the new entry first in `GCMCP_ENCRYPTION_KEYS`, keeping the old one after it: `GCMCP_ENCRYPTION_KEYS="<new>,<old>"`.
2. Restart the server.
3. Run `google-contacts-mcp rekey` against the live database. It re-encrypts every Google token with the new key.
4. Keep the old key listed for 180 days. The tokens the server issues to assistants are signed with a secret derived from the keys, so removing the old key early forces every user to reconnect.

Once the server runs, connect your assistant as described in [Connect Claude and ChatGPT](../README.md#2-connect-claude-and-chatgpt).
