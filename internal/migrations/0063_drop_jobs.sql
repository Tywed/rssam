-- The poll queue lived in jobs: INSERT by the scheduler, UPDATE on claim,
-- DELETE on completion — three statements, three transactions and 41 000
-- autovacuums a month next to the one feeds UPDATE that records the poll.
-- Only one process polls a database (advisory lock), so the set of polls in
-- flight is kept in that process's memory; a feed is due by
-- feeds.next_check_at alone and a restart simply finds the interrupted
-- feeds still due, which is what the unlogged table gave after a crash too.
DROP TABLE IF EXISTS jobs;
