ALTER TABLE shorten_urls
ADD CONSTRAINT shorten_urls_original_url_key UNIQUE (original_url);
