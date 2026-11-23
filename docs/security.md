# Security notes

- Each instance is its own OAuth 2.1 authorization server. It accepts only PKCE with S256, binds tokens to the `/mcp` resource (RFC 8707), and returns the issuer on authorization responses (RFC 9207).
- Issued tokens are opaque; the database stores only their signatures. Access tokens last 1 hour. Refresh tokens rotate on every use, and reusing an old one revokes the whole family. Unused refresh tokens expire after 180 days.
- Google tokens are encrypted with AES-256-GCM, bound to the user they belong to. Keys rotate with `genkey` and `rekey`.
- Client metadata documents are fetched over HTTPS from public addresses only, with a 5-second timeout and a 64 KiB cap. A cached document is served at most 7 days past its expiry while its host is down, and dropped as soon as the host answers 404, 410 or 400.
- Registered and fetched clients may list at most 10 redirect URIs of 2 KiB each and a name of 100 characters.
- Tools accept only `people/c<digits>` contact names and `contactGroups/<id>` group names, so no argument can reach another People API resource.
- The consent page shows the client's name and the host it will redirect you to, so a malicious client cannot pass itself off as Claude without you seeing where you will land.
- Logs never contain tokens, codes, secrets or Google responses. Metrics carry no user or client identifiers and sit behind basic authentication.
- Write tools change your real address book. Clients see `delete_contact` flagged as destructive and the read tools flagged as read-only.

Please report vulnerabilities privately through the repository's **Security** tab, not in a public issue.
