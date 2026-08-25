DROP INDEX IF EXISTS idx_shorten_urls_user_id;

ALTER TABLE shorten_urls 
DROP CONSTRAINT IF EXISTS fk_shorten_urls_user_id;

ALTER TABLE shorten_urls 
DROP COLUMN IF EXISTS user_id;

DROP TABLE IF EXISTS users;
