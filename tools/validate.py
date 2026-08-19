#!/usr/bin/env python3
"""Validate the suite tree: schema, ids, citations, index consistency.

Checks, per case file under cases/*.yaml:
  - parses as YAML and validates against case.schema.json
  - id is globally unique
  - filename's leading digits == id
  - source: cites raw.githubusercontent.com pinned to the tag in PIN

...and, for the tree as a whole, that INDEX.md's "Area <-> id band <->
fixture fragment" table accurately describes what is on disk (see the
"Adaptations from the task-7 brief" note below).

Exit 0 with "OK" on a healthy tree; exit 1 with findings printed otherwise.

Adaptations from the task-7 brief's starting script:

  1. Case ids are not uniformly 4 digits: 689 cases use 4-digit ids and 73
     use 5-digit "overflow" ids (e.g. 11800, 12400 — see INDEX.md's
     "Non-contiguous bands" note and HARNESS.md Sec.7). The brief's filename
     regex `r"(\\d+)_"` already matches either width unmodified, so no
     change was needed there; noted here since the brief's own INDEX regex
     (`\\b(1\\d{3})\\b`, 4-digit-only) would have silently ignored all 73.

  2. INDEX.md does not enumerate every case id (there is no literal id list
     to diff against). It instead publishes a machine-checkable
     "Area <-> id band <-> fixture fragment" table: one row per area giving
     a case count and one or more inclusive id-range bands (several areas
     have a second "overflow" band alongside their primary 50-wide band).
     The INDEX check below parses that table and checks its claims — per
     area (area is read from each case's own `feature:` prefix, which
     INDEX.md itself calls authoritative), band membership and case count —
     against the ids actually observed on disk, plus the grand total in the
     table's closing "Total: N cases" line. This replaces the brief's
     literal-id-membership check, which doesn't apply to a tree whose index
     documents bands rather than ids.

  3. The source-citation regex is tightened versus the brief, not loosened:
     every one of the 762 real `source:` values was inspected and all use
     exactly one shape, https://raw.githubusercontent.com/PostgREST/postgrest/
     <tag>/<path>#L<line>, where <tag> is the version pinned in PIN
     (currently v16.0 for all 762). The brief's regex accepted any
     "v<digits.dots>" tag; this version reads the actual pinned tag out of
     PIN and requires an exact match plus the #L<line> anchor, so a stray
     citation against the wrong pin (or a missing line anchor) is caught.
     case.schema.json's own `source` pattern already requires the
     raw.githubusercontent.com host and a #L<n> anchor for any tag; this
     check adds the pin-tag match on top, which the schema does not enforce.
"""
import glob
import json
import re
import sys

import jsonschema
import yaml

errors = []
schema = json.load(open("case.schema.json"))

# --- source citation: pinned tag comes from PIN, not hardcoded -------------
pin_match = re.search(r"^postgrest:\s*(\S+)", open("PIN").read(), re.MULTILINE)
if not pin_match:
    print("PIN: could not find a 'postgrest: <tag>' line", file=sys.stderr)
    sys.exit(2)
pinned_tag = pin_match.group(1)
source_re = re.compile(
    r"^https://raw\.githubusercontent\.com/PostgREST/postgrest/"
    + re.escape(pinned_tag)
    + r"/.+#L[0-9]+$"
)

seen = {}  # id -> path
area_ids = {}  # area (feature: prefix) -> set of ids found on disk
for path in sorted(glob.glob("cases/*.yaml")):
    doc = yaml.safe_load(open(path))
    try:
        jsonschema.validate(doc, schema)
    except jsonschema.ValidationError as e:
        errors.append(f"{path}: schema: {e.message}")
        continue

    cid = doc["id"]
    if cid in seen:
        errors.append(f"{path}: duplicate id {cid} (also {seen[cid]})")
    seen[cid] = path

    stem = re.match(r"(\d+)_", path.split("/")[-1])
    if not stem or int(stem.group(1)) != cid:
        errors.append(f"{path}: filename prefix != id {cid}")

    src = doc.get("source", "")
    if not source_re.match(src):
        errors.append(
            f"{path}: malformed source citation (want pin {pinned_tag!r}): {src!r}"
        )

    area = doc.get("feature", "").split("/", 1)[0]
    area_ids.setdefault(area, set()).add(cid)

# --- INDEX.md consistency ---------------------------------------------------
# Parse every 3+-column markdown table row "| area | count | id band(s) |"
# out of INDEX.md, ignoring the header/separator rows (their first two
# cells aren't a bare word + a bare integer) and any row belonging to some
# other table in the file (same reason).
index_text = open("INDEX.md").read()
row_re = re.compile(r"^\|([^|]+)\|([^|]+)\|([^|]+)\|", re.MULTILINE)
index_areas = {}
for m in row_re.finditer(index_text):
    area_cell, count_cell, band_cell = (c.strip().replace("**", "") for c in m.groups())
    if not re.fullmatch(r"[A-Za-z_]+", area_cell):
        continue
    if not re.fullmatch(r"\d+", count_cell):
        continue
    ranges = []
    for piece in re.split(r"[,+]", band_cell):
        nums = re.findall(r"\d+", piece)
        if len(nums) == 2:
            ranges.append((int(nums[0]), int(nums[1])))
        elif len(nums) == 1:
            ranges.append((int(nums[0]), int(nums[0])))
    if ranges:
        index_areas[area_cell] = {"count": int(count_cell), "ranges": ranges}

if not index_areas:
    errors.append("INDEX.md: could not parse the Area <-> id band table (0 rows matched)")

for area, ids in area_ids.items():
    claim = index_areas.get(area)
    if claim is None:
        errors.append(
            f"INDEX.md: no id-band table row for area {area!r} ({len(ids)} cases on disk)"
        )
        continue
    if len(ids) != claim["count"]:
        errors.append(
            f"INDEX.md: area {area!r} claims {claim['count']} cases, {len(ids)} found on disk"
        )
    out_of_band = sorted(
        cid for cid in ids if not any(lo <= cid <= hi for lo, hi in claim["ranges"])
    )
    if out_of_band:
        errors.append(
            f"INDEX.md: area {area!r} has ids outside its declared band(s) "
            f"{claim['ranges']}: {out_of_band[:10]}"
        )

for area in index_areas:
    if area not in area_ids:
        errors.append(
            f"INDEX.md: area {area!r} is listed in the table but no case on disk has that "
            "feature: prefix"
        )

declared_total = sum(c["count"] for c in index_areas.values())
if declared_total != len(seen):
    errors.append(
        f"INDEX.md: area counts in the table sum to {declared_total}, "
        f"{len(seen)} cases found on disk"
    )

total_line = re.search(r"^Total:\s*\**(\d+)\s*cases\**", index_text, re.MULTILINE)
if not total_line:
    errors.append("INDEX.md: could not find the 'Total: N cases' summary line")
elif int(total_line.group(1)) != len(seen):
    errors.append(
        f"INDEX.md: 'Total:' line claims {total_line.group(1)} cases, "
        f"{len(seen)} found on disk"
    )

print(f"{len(seen)} cases checked")
if errors:
    print("\n".join(errors))
    sys.exit(1)
print("OK")
