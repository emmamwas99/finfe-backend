-- The existing web app encrypts its document in the browser. The server stores
-- ciphertext only; normalized finance tables are separate API resources.
CREATE TABLE vaults (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    kdf JSONB NOT NULL,
    wrapped_key JSONB NOT NULL,
    recovery_key JSONB NOT NULL,
    data JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
