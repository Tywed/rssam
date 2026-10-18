-- 0045 set toast_tuple_target = 128 on entries so that mark-read (then an
-- UPDATE of the entries row) rewrote a small tuple. Since 0058 read state
-- lives in user_unread/user_entries and an entries row is never updated, but
-- the 128 B target still pushed every column of a new entry out of line:
-- url (29 B), hash (64 B), title, content (208 B compressed), search_vector
-- - five TOAST rows plus five TOAST index entries per entry. Default target
-- (2 kB): 7 -> 2 rows per entry, WAL per poll with new entries 3 360 -> 2 646 B,
-- entries table -25 %, half the buffers per list page. Rows written so far
-- stay as they are; new rows get the inline layout.
ALTER TABLE entries RESET (toast_tuple_target);
