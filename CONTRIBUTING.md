# Contributing

This suite records what PostgREST actually does. It is not a spec of what a
server *should* do, and it does not encode anyone's opinion about correct
REST design — every expectation traces back to a real PostgREST behavior.

## Ground rule: cases record PostgREST behavior only

Every case in `cases/` and every entry in `spec/*.yaml` must carry a
`source:` URL (a `raw.githubusercontent.com/PostgREST/postgrest/...#Lnnn`
line anchor, pinned to the version in `PIN`) that a reader can re-fetch and
verify supports the claim. If a behavior can't be traced to a real source
line, it doesn't belong here — drop it rather than guess. Do not add cases
that assert what you think PostgREST *should* do; only what it *does* do,
proven by a citation.

The `preconditions:` case field from earlier drafts of this format is
retired. State any assumptions in `notes:` instead, e.g. `"Assumes: default
db-max-rows"`.

## Changing expected behavior

Case expectations change for exactly one reason: PostgREST's behavior
changed at a new pinned version. That re-sync happens only through the
workflows in `.claude/workflows/` (`bier-spec` for a full re-sync,
`bier-spec-audit` for a citation-only audit) run against a new `PIN`. Do not
hand-edit an existing case's `expect:` block outside that process — if you
believe an expectation is wrong, that's a signal to re-verify the citation
via `bier-spec-audit`, not to patch the YAML directly.

## Fixture changes

- New fixture objects needed by new/changed cases go in
  `fixtures/provenance/<area>.delta.sql`, which is later folded into
  `fixtures/02_base.sql` by a reviewed change (see `fixtures/README.md`).
- `fixtures/inputs/rpc.sql` and `fixtures/inputs/headers.sql` are live
  generator inputs and `fixtures/03_supplement.sql` is a human-owned
  supplement — all three are edited directly, but only through reviewed
  commits.
- Never hand-edit `fixtures/02_base.sql` wholesale or any file under
  `fixtures/provenance/` other than via its `.delta.sql` channel.

**After any fixture change**, regenerate the derived area-schema mirror and
commit the result:

```sh
elixir tools/regen_area_schemas.exs
```

This regenerates `fixtures/06_area_schemas.sql` from `fixtures/inputs/` +
`fixtures/02_base.sql`/`fixtures/03_supplement.sql`. Do not hand-edit
`06_area_schemas.sql`.

## Before every PR

```sh
make -C tools/oracle validate
```

This validates every case against `case.schema.json`, checks id uniqueness,
and runs the other machine checks described in `HARNESS.md`. A PR that
doesn't pass it will fail review.

(`tools/validate.py` is the same check's Python predecessor; CI still runs
both during the transition, and either command is fine locally. The Python
version needs `pip install pyyaml jsonschema`; the Go one only needs Go.)

## Divergences belong to consumers, not here

This suite records reference (PostgREST) behavior unconditionally. If your
implementation deliberately diverges from PostgREST on some case, that skip
list lives in *your* project, not here — see `HARNESS.md` for how bier
itself maintains one. Do not add a skip/pending mechanism to this repo.
