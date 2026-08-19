-- Refresh planner statistics across the whole database. Planner-estimate
-- cases (`count=planned`/`count=estimated`) assume analyzed tables, so this
-- must run after every chain file that inserts data (02-06) and before the
-- suite is exercised.
ANALYZE;
