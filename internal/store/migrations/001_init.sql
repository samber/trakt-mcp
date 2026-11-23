-- Times are Unix seconds (UTC). Secrets are stored only as crypto.Keyring ciphertext.
-- Foreign keys have no ON DELETE action: Store.DeleteUser removes child rows
-- explicitly in one transaction, and the keys reject any row it would orphan.

CREATE TABLE users (
    id           TEXT    PRIMARY KEY,
    google_sub   TEXT    NOT NULL UNIQUE,
    email        TEXT    NOT NULL,
    created_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL
) STRICT;

CREATE INDEX users_email ON users (email);
CREATE INDEX users_last_seen_at ON users (last_seen_at);

CREATE TABLE google_tokens (
    user_id    TEXT    PRIMARY KEY REFERENCES users (id),
    refresh_ct BLOB    NOT NULL,
    access_ct  BLOB    NOT NULL,
    -- 0 when Google gave no expiry.
    expiry     INTEGER NOT NULL,
    -- Active key id at write time; rekey reads the ciphertext header, not this column.
    key_id     TEXT    NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
) STRICT;

CREATE TABLE oauth_clients (
    id           TEXT    PRIMARY KEY,
    kind         TEXT    NOT NULL CHECK (kind IN ('cimd', 'dcr')),
    metadata     TEXT    NOT NULL CHECK (json_valid(metadata)),
    fetched_at   INTEGER,
    expires_at   INTEGER,
    last_used_at INTEGER,
    created_at   INTEGER NOT NULL DEFAULT (unixepoch())
) STRICT;

-- fosite keys PKCE sessions by the authorization code signature, so one
-- signature can appear under two kinds: the key is (kind, signature).
CREATE TABLE oauth_tokens (
    signature  TEXT    NOT NULL,
    kind       TEXT    NOT NULL CHECK (kind IN ('code', 'access', 'refresh', 'pkce')),
    request_id TEXT    NOT NULL,
    -- NULL until the session is bound to a user.
    user_id    TEXT    REFERENCES users (id),
    client_id  TEXT    NOT NULL,
    data       BLOB    NOT NULL,
    expires_at INTEGER NOT NULL,
    active     INTEGER NOT NULL DEFAULT 1,
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (kind, signature)
) STRICT;

CREATE INDEX oauth_tokens_request_id ON oauth_tokens (request_id);
-- Lets DeleteUser find a user's rows without a full scan.
CREATE INDEX oauth_tokens_user_id ON oauth_tokens (user_id);
CREATE INDEX oauth_tokens_expires_at ON oauth_tokens (expires_at);

CREATE TABLE pending_authorizations (
    id         TEXT    PRIMARY KEY,
    data       BLOB    NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL DEFAULT (unixepoch())
) STRICT;

CREATE INDEX pending_authorizations_expires_at ON pending_authorizations (expires_at);
