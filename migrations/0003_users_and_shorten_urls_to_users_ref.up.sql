CREATE TABLE IF NOT EXISTS users (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    created_at   timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE shorten_urls 
ADD COLUMN user_id uuid;

-- Из-за ограничений процедур зачистки тестовых сценариев Практикума в GitHub Actions
-- придется пока отказаться от выстановки FK, т.к. при проверке ранних спринтов
--ALTER TABLE shorten_urls
--ADD CONSTRAINT fk_shorten_urls_user_id FOREIGN KEY (user_id) REFERENCES users(id);

CREATE INDEX idx_shorten_urls_user_id ON shorten_urls(user_id);
