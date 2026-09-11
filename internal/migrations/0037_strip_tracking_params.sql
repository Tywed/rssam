-- 0.1.11: model.NormalizeURL drops tracking query parameters (utm_*, yclid,
-- fbclid, ...) before hashing, so the same article shared with different
-- campaign tags dedups to one row. Rows stored earlier still carry the
-- parameters in url and in the hash; without this rewrite every such article
-- would be inserted once more the next time the feed repeats it with other
-- tags (or with none), and the stored links would keep leaking the tags.
--
-- Same rule as the Go code at 0.1.11: match on the lower-cased key before
-- the first '=', keep the order and encoding of the remaining parameters,
-- drop '?' when nothing is left. Only http(s) URLs with a query are looked at.
-- The hash is recomputed only when it was derived from the URL
-- (hash = sha256(url)); bridge entries hashed by post id keep their hash.
-- When two rows collapse to one hash the oldest row survives: it takes the
-- labels, the star and the enclosures it lacks from the others, which are
-- deleted (their filter_matches and webhook_logs cascade).

CREATE FUNCTION pg_temp.rssam_is_tracking_param(key text) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
  SELECT lower(key) ~ '^(utm_|mtm_|pk_|itm_|hmb_)'
      OR lower(key) IN (
        'gclid', 'dclid', 'gbraid', 'wbraid', 'gclsrc', 'srsltid', '_ga', '_gl',
        'yclid', 'ysclid', '_openstat',
        'fbclid', 'fb_action_ids', 'fb_action_types', 'fb_ref', 'fb_source', 'fb_comment_id',
        'igshid', 'ttclid', 'twclid', 'ref_src', 'ref_url', 'li_fat_id', 'msclkid',
        'mc_cid', 'mc_eid', 'mc_tc',
        '_hsenc', '_hsmi', '__hssc', '__hstc', '__hsfp', 'hsctatracking', 'hsa_cam',
        'mkt_tok', 'sc_cid', '_bhlid', 'vero_id', 'vero_conv',
        'oly_anon_id', 'oly_enc_id', 'rb_clickid', 'wickedid',
        '_branch_match_id', '_branch_referrer')
$$;

-- Returns u unchanged when it has no tracking parameter.
CREATE FUNCTION pg_temp.rssam_strip_tracking(u text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  WITH parts AS (
    SELECT left(u, strpos(u, '?') - 1) AS base, substr(u, strpos(u, '?') + 1) AS q
  ), segs AS (
    SELECT seg, ord, pg_temp.rssam_is_tracking_param(split_part(seg, '=', 1)) AS tracking
    FROM parts, regexp_split_to_table(parts.q, '&') WITH ORDINALITY AS t(seg, ord)
  )
  SELECT CASE
    WHEN u !~* '^https?://' OR strpos(u, '?') = 0 THEN u
    WHEN NOT EXISTS (SELECT 1 FROM segs WHERE tracking) THEN u
    ELSE parts.base || COALESCE('?' || (
      SELECT string_agg(seg, '&' ORDER BY ord) FROM segs WHERE NOT tracking AND seg <> ''), '')
  END
  FROM parts
$$;

CREATE FUNCTION pg_temp.rssam_url_hash(u text) RETURNS text
LANGUAGE sql IMMUTABLE AS $$
  SELECT encode(sha256(convert_to(u, 'UTF8')), 'hex')
$$;

-- Prefilter with one cheap regex so that the per-row function runs only on
-- candidates; the function itself is the source of truth (false positives
-- of the prefilter are dropped by new_url <> url).
CREATE TEMP TABLE rssam_rw ON COMMIT DROP AS
SELECT e.id, e.feed_id, e.hash, c.new_url,
       CASE WHEN e.hash = pg_temp.rssam_url_hash(e.url) THEN pg_temp.rssam_url_hash(c.new_url) ELSE e.hash END AS new_hash
FROM entries e, LATERAL (SELECT pg_temp.rssam_strip_tracking(e.url) AS new_url) c
WHERE e.url ~* '[?&](utm_|mtm_|pk_|itm_|hmb_|(gclid|dclid|gbraid|wbraid|gclsrc|srsltid|_ga|_gl|yclid|ysclid|_openstat|fbclid|fb_action_ids|fb_action_types|fb_ref|fb_source|fb_comment_id|igshid|ttclid|twclid|ref_src|ref_url|li_fat_id|msclkid|mc_cid|mc_eid|mc_tc|_hsenc|_hsmi|__hssc|__hstc|__hsfp|hsctatracking|hsa_cam|mkt_tok|sc_cid|_bhlid|vero_id|vero_conv|oly_anon_id|oly_enc_id|rb_clickid|wickedid|_branch_match_id|_branch_referrer)(=|&|$))'
  AND c.new_url <> e.url;

CREATE INDEX ON rssam_rw (feed_id, new_hash);

-- Every row that will carry (feed_id, new_hash) after the rewrite: the
-- rewritten ones and the untouched ones that already have that hash.
CREATE TEMP TABLE rssam_groups ON COMMIT DROP AS
SELECT feed_id, new_hash, id FROM rssam_rw
UNION
SELECT e.feed_id, e.hash, e.id
FROM entries e
WHERE EXISTS (SELECT 1 FROM rssam_rw r WHERE r.feed_id = e.feed_id AND r.new_hash = e.hash)
  AND NOT EXISTS (SELECT 1 FROM rssam_rw r WHERE r.id = e.id);

CREATE TEMP TABLE rssam_losers ON COMMIT DROP AS
SELECT g.id, w.winner_id
FROM rssam_groups g
JOIN (SELECT feed_id, new_hash, min(id) AS winner_id FROM rssam_groups GROUP BY feed_id, new_hash HAVING count(*) > 1) w
  ON w.feed_id = g.feed_id AND w.new_hash = g.new_hash
WHERE g.id <> w.winner_id;

INSERT INTO entry_labels (entry_id, label_id, created_at)
SELECT l.winner_id, el.label_id, el.created_at
FROM entry_labels el
JOIN rssam_losers l ON l.id = el.entry_id
ON CONFLICT DO NOTHING;

UPDATE entries e SET starred = true
WHERE NOT e.starred
  AND EXISTS (SELECT 1 FROM rssam_losers l JOIN entries d ON d.id = l.id WHERE l.winner_id = e.id AND d.starred);

UPDATE enclosures en SET entry_id = l.winner_id
FROM rssam_losers l
WHERE en.entry_id = l.id
  AND NOT EXISTS (SELECT 1 FROM enclosures w WHERE w.entry_id = l.winner_id AND w.url = en.url);

DELETE FROM entries e USING rssam_losers l WHERE e.id = l.id;

UPDATE entries e SET url = r.new_url, hash = r.new_hash
FROM rssam_rw r
WHERE e.id = r.id;

-- feed_entry_dedup has no dependants: drop the affected rows and put back one
-- per new hash, the earliest first_seen_at winning.
CREATE TEMP TABLE rssam_rwd ON COMMIT DROP AS
SELECT d.feed_id, d.hash, d.first_seen_at, c.new_url,
       CASE WHEN d.hash = pg_temp.rssam_url_hash(d.url) THEN pg_temp.rssam_url_hash(c.new_url) ELSE d.hash END AS new_hash
FROM feed_entry_dedup d, LATERAL (SELECT pg_temp.rssam_strip_tracking(d.url) AS new_url) c
WHERE d.url ~* '[?&](utm_|mtm_|pk_|itm_|hmb_|(gclid|dclid|gbraid|wbraid|gclsrc|srsltid|_ga|_gl|yclid|ysclid|_openstat|fbclid|fb_action_ids|fb_action_types|fb_ref|fb_source|fb_comment_id|igshid|ttclid|twclid|ref_src|ref_url|li_fat_id|msclkid|mc_cid|mc_eid|mc_tc|_hsenc|_hsmi|__hssc|__hstc|__hsfp|hsctatracking|hsa_cam|mkt_tok|sc_cid|_bhlid|vero_id|vero_conv|oly_anon_id|oly_enc_id|rb_clickid|wickedid|_branch_match_id|_branch_referrer)(=|&|$))'
  AND c.new_url <> d.url;

DELETE FROM feed_entry_dedup d USING rssam_rwd r WHERE d.feed_id = r.feed_id AND d.hash = r.hash;

INSERT INTO feed_entry_dedup (feed_id, hash, url, first_seen_at)
SELECT DISTINCT ON (feed_id, new_hash) feed_id, new_hash, new_url, first_seen_at
FROM rssam_rwd
ORDER BY feed_id, new_hash, first_seen_at
ON CONFLICT (feed_id, hash) DO UPDATE SET first_seen_at = LEAST(feed_entry_dedup.first_seen_at, EXCLUDED.first_seen_at);

DROP FUNCTION pg_temp.rssam_strip_tracking(text);
DROP FUNCTION pg_temp.rssam_is_tracking_param(text);
DROP FUNCTION pg_temp.rssam_url_hash(text);
