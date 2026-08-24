// Package validate implements the suite tree check (`oracle validate`),
// the Go port of — and since the v16.0.0-suite.3 transition cycle the sole
// successor to — the retired tools/validate.py. Per case file under
// cases/*.yaml it checks that the file parses as YAML, validates against
// case.schema.json (draft 2020-12), loads through the runner's own strict
// loader (internal/cases), has a globally-unique id matching its filename
// prefix, and cites a raw.githubusercontent.com source pinned to the tag
// in PIN; for the tree as a whole it checks the "Area <-> id band <->
// fixture fragment" tables in INDEX.md and HARNESS.md §7, and the CLI-set
// claims those two documents plus COVERAGE.md and spec/README.md state in
// running prose (see clishape.go), against what is on disk.
//
// YAML dialect note (inherited from the validate.py transition, and still
// load-bearing for suite consumers on other parsers): yaml.v3 parses
// YAML 1.2 scalars where YAML 1.1 parsers such as pyyaml differ, so
// unquoted `yes`/`no`/`on`/`off` stay strings here (a 1.1 parser resolves
// them to booleans) and unquoted dates become time.Time (serialized to an
// RFC3339 string for schema purposes) rather than a plain string. This
// validator therefore accepts a few scalar spellings a 1.1-based
// validator would flag — deliberately: it matches what the runner's own
// parser sees when it executes the case. The corpus itself is guarded
// against cross-dialect drift by internal/cases's pyyaml cross-check
// test, which keeps every published case parsing identically under both
// dialects (it skips where python3+pyyaml are absent; CI's tree job
// always runs it).
//
// On YAML that does not parse at all, the validator emits a per-file
// finding and keeps checking the rest of the tree (validate.py crashed
// with an uncaught traceback there, losing every other finding).
package validate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
)

// Result is the outcome of a tree check. Findings are human-readable
// violations (empty on a healthy tree); CasesChecked counts the case files
// examined.
type Result struct {
	CasesChecked int
	Findings     []string
}

var (
	pinRe      = regexp.MustCompile(`(?m)^postgrest:\s*(\S+)`)
	filenameRe = regexp.MustCompile(`^(\d+)_`)
)

// Tree checks the suite tree rooted at root. A non-nil error means the
// check itself could not run (missing PIN, unreadable schema, ...), as
// distinct from findings, which are violations in the tree.
func Tree(root string) (Result, error) {
	var res Result

	pinnedTag, err := pinnedTag(filepath.Join(root, "PIN"))
	if err != nil {
		return res, err
	}
	sourceRe := regexp.MustCompile(
		`^https://raw\.githubusercontent\.com/PostgREST/postgrest/` +
			regexp.QuoteMeta(pinnedTag) + `/.+#L[0-9]+$`)

	schema, err := compileSchema(filepath.Join(root, "case.schema.json"))
	if err != nil {
		return res, err
	}

	files, err := filepath.Glob(filepath.Join(root, "cases", "*.yaml"))
	if err != nil {
		return res, err
	}
	sort.Strings(files)

	seen := map[int]string{}             // id -> path
	areaIDs := map[string]map[int]bool{} // area (feature: prefix) -> ids on disk
	cli := cliFacts{ids: map[int]bool{}, flags: map[string]int{}}

	for _, path := range files {
		res.CasesChecked++
		rel := relPath(root, path)

		doc, err := cases.RawDocument(path)
		if err != nil {
			res.Findings = append(res.Findings, fmt.Sprintf("%s: %s", rel, errorText(err, path)))
			continue
		}
		if err := validateAgainstSchema(schema, doc); err != nil {
			res.Findings = append(res.Findings, fmt.Sprintf("%s: schema: %s", rel, schemaErrorText(err)))
			continue
		}

		// The runner's own loader is stricter than the schema in places
		// (e.g. request-shape cross-field rules); a tree that validates but
		// cannot load is still broken.
		c, err := cases.Load(path)
		if err != nil {
			res.Findings = append(res.Findings, fmt.Sprintf("%s: loader: %s", rel, errorText(err, path)))
			continue
		}

		if prev, dup := seen[c.ID]; dup {
			res.Findings = append(res.Findings, fmt.Sprintf("%s: duplicate id %d (also %s)", rel, c.ID, prev))
		}
		seen[c.ID] = rel

		m := filenameRe.FindStringSubmatch(filepath.Base(path))
		if m == nil {
			res.Findings = append(res.Findings, fmt.Sprintf("%s: filename prefix != id %d", rel, c.ID))
		} else if n, _ := strconv.Atoi(m[1]); n != c.ID {
			res.Findings = append(res.Findings, fmt.Sprintf("%s: filename prefix != id %d", rel, c.ID))
		}

		if !sourceRe.MatchString(c.Source) {
			res.Findings = append(res.Findings, fmt.Sprintf(
				"%s: malformed source citation (want pin %q): %q", rel, pinnedTag, c.Source))
		}

		if areaIDs[c.Area] == nil {
			areaIDs[c.Area] = map[int]bool{}
		}
		areaIDs[c.Area][c.ID] = true

		if c.Request.Kind == "cli" {
			cli.ids[c.ID] = true
			cli.flags[c.Request.Flag]++
		}
	}

	tableFindings, err := checkAreaTables(root, areaIDs, len(seen))
	if err != nil {
		return res, err
	}
	res.Findings = append(res.Findings, tableFindings...)

	cliFindings, err := checkCLIShape(root, cli)
	if err != nil {
		return res, err
	}
	res.Findings = append(res.Findings, cliFindings...)

	return res, nil
}

var (
	rowRe        = regexp.MustCompile(`(?m)^\|([^|]+)\|([^|]+)\|([^|]+)\|`)
	areaRe       = regexp.MustCompile(`^[A-Za-z_]+$`)
	countRe      = regexp.MustCompile(`^\d+$`)
	numRe        = regexp.MustCompile(`\d+`)
	pieceRe      = regexp.MustCompile(`[,+]`)
	bandNoteRe   = regexp.MustCompile(`\([^)]*\)`)
	totalLineRe  = regexp.MustCompile(`(?m)^\*{0,2}Total:.*$`)
	totalCasesRe = regexp.MustCompile(`\**(\d+)\**\s*cases`)
	totalAreasRe = regexp.MustCompile(`\**(\d+)\**\s*areas`)
	breakdownRe  = regexp.MustCompile(`\(\s*((?:\d+\s*\+\s*)+\d+)\s*=\s*(\d+)\s*\)`)
	headingRe    = regexp.MustCompile(`(?m)^## `)

	harnessSectionRe = regexp.MustCompile(`(?m)^## 7\. Areas\s*$`)
	overflowNoteRe   = regexp.MustCompile(`(?m)^> \*\*([A-Za-z]+) areas overflow into a 5-digit band\*\*`)
	overflowAreaRe   = regexp.MustCompile("`([A-Za-z_]+)` \\((\\d{5})\\+\\)")
)

// numberWords spells the small integers the way HARNESS.md's overflow note
// does ("Four areas overflow ..."). Only the range a 17-area tree can
// plausibly reach is covered; a count outside it reports as a mismatch
// rather than silently passing.
var numberWords = []string{
	"zero", "one", "two", "three", "four", "five", "six", "seven", "eight",
	"nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen",
	"sixteen", "seventeen", "eighteen", "nineteen", "twenty",
}

// overflowBand is the first id of the 5-digit band an area overflows into.
const overflowBand = 10000

type idRange struct{ lo, hi int }

// String renders the range the way INDEX.md spells it ("100-149", or a bare
// "100" for a single-id band), so the out-of-band finding reads like the
// table row it contradicts. (validate.py prints Python tuples here —
// "[(100, 149)]" — which the parity tests tolerate: they compare verdicts,
// not finding text.)
func (r idRange) String() string {
	if r.lo == r.hi {
		return strconv.Itoa(r.lo)
	}
	return fmt.Sprintf("%d-%d", r.lo, r.hi)
}

type areaClaim struct {
	count  int
	ranges []idRange
}

// areaTable describes one document's "area <-> id band <-> case count"
// table. Both documents lead each row with the area name but order the
// other two cells differently: INDEX.md spells the row
// "| Area | Cases | Id band |", HARNESS.md §7 spells it
// "| Area | Id band(s) | Cases |".
//
// Both are checked because both are contracts carrying per-area counts,
// and they diverge silently otherwise: the 762 -> 801 pass refreshed
// INDEX.md's table and missed HARNESS.md's, which nothing caught because
// only INDEX.md was ever parsed — even though HARNESS.md §7 is the table
// declared authoritative for routing which schema an id needs.
type areaTable struct {
	doc       string         // file name, as it appears in finding text
	section   *regexp.Regexp // if set, parse only this section, not the whole file
	countCell int            // 1-based index into a row's first three cells
	bandCell  int
}

var areaTables = []areaTable{
	{doc: "INDEX.md", countCell: 2, bandCell: 3},
	{doc: "HARNESS.md", section: harnessSectionRe, countCell: 3, bandCell: 2},
}

// checkAreaTables checks every document's area table against the ids
// observed on disk, in the order declared above.
func checkAreaTables(root string, areaIDs map[string]map[int]bool, totalOnDisk int) ([]string, error) {
	var findings []string
	for _, tbl := range areaTables {
		f, err := checkAreaTable(tbl, filepath.Join(root, tbl.doc), areaIDs, totalOnDisk)
		if err != nil {
			return nil, err
		}
		findings = append(findings, f...)
	}
	return findings, nil
}

// checkAreaTable checks one document's "Area <-> id band <-> fixture
// fragment" table — per-area case count and band membership, plus the
// closing "Total: N cases" line and, where the document spells one out,
// the arithmetic breakdown beside it — against the ids observed on disk.
// The INDEX.md half is a direct port of validate.py's INDEX.md section,
// findings text included.
func checkAreaTable(tbl areaTable, path string, areaIDs map[string]map[int]bool, totalOnDisk int) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := string(b)

	var findings []string

	if tbl.section != nil {
		section, ok := sliceSection(text, tbl.section)
		if !ok {
			return []string{fmt.Sprintf(
				"%s: could not find the area-table section (%s)", tbl.doc, tbl.section)}, nil
		}
		text = section
	}

	// Parse every 3+-column markdown table row, ignoring header/separator
	// rows and rows of other tables in the same span (their area and count
	// cells aren't a bare word and a bare integer).
	declared := map[string]areaClaim{}
	for _, m := range rowRe.FindAllStringSubmatch(text, -1) {
		areaCell := cellText(m[1])
		countCell := cellText(m[tbl.countCell])
		bandCell := cellText(m[tbl.bandCell])
		if !areaRe.MatchString(areaCell) || !countRe.MatchString(countCell) {
			continue
		}
		// A band cell may annotate a range with a parenthetical
		// ("11407-11415 (no 11406)"); its digits are commentary on the
		// range, not part of it, so they must not be read as bounds.
		var ranges []idRange
		for _, piece := range pieceRe.Split(bandNoteRe.ReplaceAllString(bandCell, ""), -1) {
			nums := numRe.FindAllString(piece, -1)
			switch len(nums) {
			case 2:
				lo, _ := strconv.Atoi(nums[0])
				hi, _ := strconv.Atoi(nums[1])
				ranges = append(ranges, idRange{lo, hi})
			case 1:
				n, _ := strconv.Atoi(nums[0])
				ranges = append(ranges, idRange{n, n})
			}
		}
		if len(ranges) > 0 {
			count, _ := strconv.Atoi(countCell)
			declared[areaCell] = areaClaim{count: count, ranges: ranges}
		}
	}

	if len(declared) == 0 {
		findings = append(findings, fmt.Sprintf(
			"%s: could not parse the Area <-> id band table (0 rows matched)", tbl.doc))
	}

	for _, area := range sortedKeys(areaIDs) {
		ids := areaIDs[area]
		claim, ok := declared[area]
		if !ok {
			findings = append(findings, fmt.Sprintf(
				"%s: no id-band table row for area %q (%d cases on disk)", tbl.doc, area, len(ids)))
			continue
		}
		if len(ids) != claim.count {
			findings = append(findings, fmt.Sprintf(
				"%s: area %q claims %d cases, %d found on disk", tbl.doc, area, claim.count, len(ids)))
		}
		var outOfBand []int
		for id := range ids {
			inBand := false
			for _, r := range claim.ranges {
				if r.lo <= id && id <= r.hi {
					inBand = true
				}
			}
			if !inBand {
				outOfBand = append(outOfBand, id)
			}
		}
		if len(outOfBand) > 0 {
			sort.Ints(outOfBand)
			if len(outOfBand) > 10 {
				outOfBand = outOfBand[:10]
			}
			findings = append(findings, fmt.Sprintf(
				"%s: area %q has ids outside its declared band(s) %v: %v", tbl.doc, area, claim.ranges, outOfBand))
		}
	}

	for _, area := range sortedKeys(declared) {
		if _, ok := areaIDs[area]; !ok {
			findings = append(findings, fmt.Sprintf(
				"%s: area %q is listed in the table but no case on disk has that feature: prefix", tbl.doc, area))
		}
	}

	declaredTotal := 0
	for _, claim := range declared {
		declaredTotal += claim.count
	}
	if declaredTotal != totalOnDisk {
		findings = append(findings, fmt.Sprintf(
			"%s: area counts in the table sum to %d, %d cases found on disk", tbl.doc, declaredTotal, totalOnDisk))
	}

	findings = append(findings, checkTotalLine(tbl, text, len(areaIDs), totalOnDisk)...)

	if tbl.doc == "HARNESS.md" {
		findings = append(findings, checkOverflowNote(tbl, text, declared)...)
	}

	return findings, nil
}

// checkTotalLine checks the document's "Total: N cases" summary — the case
// count, the area count where the line states one, and the arithmetic
// breakdown where it spells one out ("(36+87+... = 762)"). A breakdown
// whose addends do not sum to its own stated total is reported separately
// from one that sums correctly to the wrong number, because the two mean
// different mistakes.
func checkTotalLine(tbl areaTable, text string, areasOnDisk, totalOnDisk int) []string {
	var findings []string

	line := totalLineRe.FindString(text)
	if line == "" {
		return []string{fmt.Sprintf("%s: could not find the 'Total: N cases' summary line", tbl.doc)}
	}

	if m := totalCasesRe.FindStringSubmatch(line); m == nil {
		findings = append(findings, fmt.Sprintf(
			"%s: 'Total:' line does not state a case count: %q", tbl.doc, oneLine(line)))
	} else if n, _ := strconv.Atoi(m[1]); n != totalOnDisk {
		findings = append(findings, fmt.Sprintf(
			"%s: 'Total:' line claims %d cases, %d found on disk", tbl.doc, n, totalOnDisk))
	}

	if m := totalAreasRe.FindStringSubmatch(line); m != nil {
		if n, _ := strconv.Atoi(m[1]); n != areasOnDisk {
			findings = append(findings, fmt.Sprintf(
				"%s: 'Total:' line claims %d areas, %d found on disk", tbl.doc, n, areasOnDisk))
		}
	}

	if m := breakdownRe.FindStringSubmatch(line); m != nil {
		sum := 0
		for _, addend := range numRe.FindAllString(m[1], -1) {
			n, _ := strconv.Atoi(addend)
			sum += n
		}
		stated, _ := strconv.Atoi(m[2])
		switch {
		case sum != stated:
			findings = append(findings, fmt.Sprintf(
				"%s: 'Total:' line breakdown adds up to %d, not the %d it states", tbl.doc, sum, stated))
		case stated != totalOnDisk:
			findings = append(findings, fmt.Sprintf(
				"%s: 'Total:' line breakdown claims %d cases, %d found on disk", tbl.doc, stated, totalOnDisk))
		}
	}

	return findings
}

// checkOverflowNote checks HARNESS.md §7's admonition naming the areas that
// spilled past their primary 50-wide band into a 5-digit one. It is prose,
// but it is the prose an implementer reads to know a 5-digit id is not a
// typo, and it drifts exactly when the table it sits above does: the note
// still said "Four areas" after select became the fifth.
func checkOverflowNote(tbl areaTable, text string, declared map[string]areaClaim) []string {
	loc := overflowNoteRe.FindStringSubmatchIndex(text)
	if loc == nil {
		return []string{fmt.Sprintf(
			"%s: could not find the '<N> areas overflow into a 5-digit band' note", tbl.doc)}
	}

	// Everything the note claims lives in its own blockquote.
	note := text[loc[0]:]
	if end := strings.Index(note, "\n\n"); end != -1 {
		note = note[:end]
	}

	// What the table itself says overflowed: the lowest 5-digit band start
	// of every area that declares one.
	wantAreas := map[string]int{}
	for area, claim := range declared {
		for _, r := range claim.ranges {
			if r.lo < overflowBand {
				continue
			}
			if lo, ok := wantAreas[area]; !ok || r.lo < lo {
				wantAreas[area] = r.lo
			}
		}
	}

	var findings []string

	word := strings.ToLower(text[loc[2]:loc[3]])
	if want := numberWord(len(wantAreas)); word != want {
		findings = append(findings, fmt.Sprintf(
			"%s: overflow note says %q areas overflow into a 5-digit band, the table shows %d (%s)",
			tbl.doc, word, len(wantAreas), want))
	}

	gotAreas := map[string]int{}
	for _, m := range overflowAreaRe.FindAllStringSubmatch(note, -1) {
		band, _ := strconv.Atoi(m[2])
		gotAreas[m[1]] = band
	}
	for _, area := range sortedKeys(wantAreas) {
		band, ok := gotAreas[area]
		if !ok {
			findings = append(findings, fmt.Sprintf(
				"%s: overflow note does not name area %q, whose table row declares the 5-digit band %d+",
				tbl.doc, area, wantAreas[area]))
		} else if band != wantAreas[area] {
			findings = append(findings, fmt.Sprintf(
				"%s: overflow note gives area %q the band %d+, its table row declares %d+",
				tbl.doc, area, band, wantAreas[area]))
		}
	}
	for _, area := range sortedKeys(gotAreas) {
		if _, ok := wantAreas[area]; !ok {
			findings = append(findings, fmt.Sprintf(
				"%s: overflow note names area %q, whose table row declares no 5-digit band", tbl.doc, area))
		}
	}

	return findings
}

// numberWord spells n the way HARNESS.md's prose does, falling back to the
// digits for counts past the spelled-out range.
func numberWord(n int) string {
	if n >= 0 && n < len(numberWords) {
		return numberWords[n]
	}
	return strconv.Itoa(n)
}

// cellText normalizes one markdown table cell: trimmed, with the bold
// markers the tables use to highlight recently-changed rows removed.
func cellText(cell string) string {
	return strings.ReplaceAll(strings.TrimSpace(cell), "**", "")
}

// sliceSection returns the part of text between the heading matching start
// and the next "## " heading (or the end of the document), so a document
// with several markdown tables can have just one of them parsed.
func sliceSection(text string, start *regexp.Regexp) (string, bool) {
	loc := start.FindStringIndex(text)
	if loc == nil {
		return "", false
	}
	rest := text[loc[1]:]
	if end := headingRe.FindStringIndex(rest); end != nil {
		return rest[:end[0]], true
	}
	return rest, true
}

// sortedKeys returns the map's keys sorted, so findings print in a
// deterministic order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pinnedTag reads the `postgrest: <tag>` line out of the PIN file.
func pinnedTag(pinPath string) (string, error) {
	b, err := os.ReadFile(pinPath)
	if err != nil {
		return "", err
	}
	m := pinRe.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("%s: could not find a 'postgrest: <tag>' line", pinPath)
	}
	return string(m[1]), nil
}

// compileSchema loads case.schema.json into a draft-2020-12 validator.
// Formats are annotation-only (not asserted), matching Python jsonschema's
// default behavior.
func compileSchema(path string) (*jsonschema.Schema, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("case.schema.json", doc); err != nil {
		return nil, err
	}
	return compiler.Compile("case.schema.json")
}

// validateAgainstSchema round-trips the YAML-decoded document through JSON
// so the validator sees the exact value shapes (json.Number numerics) it is
// specified against, then validates it.
func validateAgainstSchema(schema *jsonschema.Schema, doc any) error {
	b, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("not JSON-representable: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	return schema.Validate(inst)
}

// schemaErrorText renders a jsonschema.ValidationError as its leaf causes
// only ("at '/expect': ..."), one line, without the compiler's absolute
// file:// schema URL prefix.
func schemaErrorText(err error) string {
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return oneLine(err.Error())
	}
	printer := message.NewPrinter(language.English)
	var parts []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			parts = append(parts, fmt.Sprintf("at '%s': %s", loc, e.ErrorKind.LocalizedString(printer)))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return oneLine(strings.Join(parts, "; "))
}

// relPath renders path relative to root (like validate.py's cases/NNNN_*.yaml
// output); falls back to the absolute path if it cannot.
func relPath(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		return rel
	}
	return path
}

// oneLine collapses a multi-line error message to a single line so each
// finding prints as one line, like validate.py's output.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// errorText renders an internal/cases error for a finding line: the finding
// is already prefixed with the tree-relative path, so the absolute path the
// loader embeds in its own message is stripped rather than printed twice.
func errorText(err error, path string) string {
	return oneLine(strings.TrimPrefix(err.Error(), path+": "))
}
