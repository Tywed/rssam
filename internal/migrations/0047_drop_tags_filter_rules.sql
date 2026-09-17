-- Rules on field "tags" (refused on write since 0.1.15) never matched:
-- entries carry no tags. Drop the leftovers so the engine needs no special
-- case for them. A filter that carried such a rule is switched off first:
-- removing an always-false rule from an AND chain would otherwise turn a
-- filter that never fired into one that does, unreviewed.
UPDATE filters SET enabled = false
WHERE enabled AND id IN (SELECT filter_id FROM filter_rules WHERE field = 'tags');
DELETE FROM filter_rules WHERE field = 'tags';
