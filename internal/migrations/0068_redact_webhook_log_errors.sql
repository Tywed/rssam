-- Delivery errors recorded before 0.3.3 quote the full Telegram request URL,
-- bot token included. Scrub what is already stored; new rows are redacted
-- before they are written.
UPDATE webhook_logs
SET last_error = regexp_replace(last_error, '/bot[^/"[:space:]]+', '/bot***', 'g')
WHERE last_error ~ '/bot[^/"[:space:]]+';
UPDATE webhook_logs
SET response_snippet = regexp_replace(response_snippet, '/bot[^/"[:space:]]+', '/bot***', 'g')
WHERE response_snippet ~ '/bot[^/"[:space:]]+';
