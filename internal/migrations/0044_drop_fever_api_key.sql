-- users.fever_api_key held md5(username:password) for a Fever API that was
-- never served. A reversible digest of every password next to its bcrypt
-- hash is a liability with no consumer; drop it.
ALTER TABLE users DROP COLUMN IF EXISTS fever_api_key;
