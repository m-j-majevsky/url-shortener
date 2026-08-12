CREATE TABLE IF NOT EXISTS shorten_urls (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    token        text        NOT NULL,
    original_url text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT shorten_urls_token_key UNIQUE (token)
);
