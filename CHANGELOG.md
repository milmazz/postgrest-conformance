# Changelog

## v16.0.0-suite.2

`fixtures/06_area_schemas.sql` no longer sets `transaction_timeout` (a
PostgreSQL 17+-only GUC that pg_dump >= 17 emits in its preamble); the fixture
chain now loads on PostgreSQL 15+ instead of failing under `ON_ERROR_STOP` on
PG 15/16. The `fixtures` CI job is now matrixed over PostGIS images for PG
15/16/17 to catch this class of regression.

## v16.0.0-suite.1

Initial release: 762 conformance cases derived from PostgREST v16.0 test suite.
