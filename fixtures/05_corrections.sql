-- Post-load seed corrections that align the consolidated fixtures with values
-- asserted by conformance cases but not seeded by the merged INSERTs. These are
-- confined to the `test` schema (mirrors pick them up automatically) and must
-- be idempotent.
--
--   * `test.complex_items."field-with_sep"` is asserted to equal the row id by
--     the select cases (QuerySpec column-projection examples), but the merged
--     INSERT omits the column so it defaults to 1. Set it to match the id.
--   * `test.complex_items.arr_data`: the consolidator took mutations' seed
--     ({1,2,3} for every row), but operators' cs/cd cases (1072/1073) need
--     PostgREST's per-row {1}, {1,2}, {1,2,3} (row N -> {1..N}). Row 3 stays
--     {1,2,3}, so the select case (1100) that reads it is unaffected.
UPDATE test.complex_items SET "field-with_sep" = id;
UPDATE test.complex_items
   SET arr_data = ARRAY(SELECT generate_series(1, id))
 WHERE id IN (1, 2, 3);
