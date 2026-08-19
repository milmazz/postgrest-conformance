#!/usr/bin/env bash
# Proves fixtures/01..07 reproduce bier's loader-built database byte-for-byte.
# Usage: BIER=/path/to/bier/checkout tools/verify_equivalence.sh
set -euo pipefail
BIER="${BIER:?set BIER to a bier checkout}"
A=bier_conf_a B=bier_conf_b
( cd "$BIER" && PGDATABASE=$A mix bier.fixtures.load )
psql -d postgres -v ON_ERROR_STOP=1 -q -f fixtures/01_roles.sql
psql -d postgres -q -c "DROP DATABASE IF EXISTS $B" \
  -c "CREATE DATABASE $B TEMPLATE template0 ENCODING 'UTF8' LC_COLLATE 'C' LC_CTYPE 'C'"
for f in fixtures/0[2-7]_*.sql; do PGTZ=UTC psql -d "$B" -v ON_ERROR_STOP=1 -q -f "$f"; done
mkdir -p scratch
# pg_dump 18.4 emits randomized `\restrict <token>` / `\unrestrict <token>` guard
# lines (a distinct token per run), plus a `-- Dumped from database...` comment
# carrying a timestamp/version — all three are dump-run artifacts, not database
# content, so they must be filtered before comparing or the diff is always
# spurious.
for db in $A $B; do
  pg_dump --no-owner -d "$db" | grep -vE '^-- Dumped|^\\restrict|^\\unrestrict' > "scratch/$db.dump"
done
diff -u scratch/$A.dump scratch/$B.dump && echo "EQUIVALENT"
