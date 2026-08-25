DROP INDEX IF EXISTS idx_shorten_urls_user_id;

-- Из-за ограничений процедур зачистки тестовых сценариев Практикума в GitHub Actions
-- придется пока отказаться от выстановки FK, т.к. при проверке ранних спринтов процедура зачистки
-- не задействует down-скрипты миграции
--ALTER TABLE shorten_urls 
--DROP CONSTRAINT IF EXISTS fk_shorten_urls_user_id;

ALTER TABLE shorten_urls 
DROP COLUMN IF EXISTS user_id;

DROP TABLE IF EXISTS users;
