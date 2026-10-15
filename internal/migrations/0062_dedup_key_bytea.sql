-- feed_entry_dedup was 82 % of a relay-only database (264 MB for 802 000
-- hashes, 345 B per row): a 64-char hex sha256 as text in the heap and
-- again in the primary key, plus a url column nothing ever read. The key
-- becomes the first 128 bits of the hash as bytea (16 B instead of 65),
-- url goes away: 345 → ~177 B per row measured on the same row count.
--
-- rssam_dedup_key maps any hash string to that key: a hex sha256 (every
-- hash rssam computes itself) is decoded and truncated; anything else
-- (an inbound feed accepts the sender's Hash verbatim) is hashed first so
-- that two different strings never share a key by construction.
CREATE OR REPLACE FUNCTION rssam_dedup_key(h text) RETURNS bytea
LANGUAGE sql IMMUTABLE STRICT PARALLEL SAFE AS $$
  SELECT CASE WHEN h ~ '^[0-9a-f]{64}$' THEN substr(decode(h, 'hex'), 1, 16)
              ELSE substr(sha256(convert_to(h, 'UTF8')), 1, 16) END
$$;

CREATE TABLE feed_entry_dedup_v2 (
  feed_id BIGINT NOT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  hash BYTEA NOT NULL,
  first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (feed_id, hash)
);

INSERT INTO feed_entry_dedup_v2 (feed_id, hash, first_seen_at)
SELECT feed_id, rssam_dedup_key(hash), min(first_seen_at)
FROM feed_entry_dedup
WHERE hash <> ''
GROUP BY feed_id, rssam_dedup_key(hash);

DROP TABLE feed_entry_dedup;
ALTER TABLE feed_entry_dedup_v2 RENAME TO feed_entry_dedup;
ALTER TABLE feed_entry_dedup RENAME CONSTRAINT feed_entry_dedup_v2_pkey TO feed_entry_dedup_pkey;
ALTER TABLE feed_entry_dedup RENAME CONSTRAINT feed_entry_dedup_v2_feed_id_fkey TO feed_entry_dedup_feed_id_fkey;
CREATE INDEX feed_entry_dedup_feed_id_first_seen_idx ON feed_entry_dedup (feed_id, first_seen_at DESC);
