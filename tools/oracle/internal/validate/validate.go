// Package validate implements the suite tree check (`oracle validate`),
// the Go port of — and since the v16.0.0-suite.3 transition cycle the sole
// successor to — the retired tools/validate.py. Per case file under
// cases/*.yaml it checks that the file parses as YAML, validates against
// case.schema.json (draft 2020-12), loads through the runner's own strict
// loader (internal/cases), has a globally-unique id matching its filename
// prefix, and cites a raw.githubusercontent.com source pinned to the tag
// in PIN; for the tree as a whole it checks INDEX.md's "Area <-> id band
// <-> fixture fragment" table against what is on disk.
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
	}

	indexFindings, err := checkIndex(filepath.Join(root, "INDEX.md"), areaIDs, len(seen))
	if err != nil {
		return res, err
	}
	res.Findings = append(res.Findings, indexFindings...)

	return res, nil
}

var (
	rowRe   = regexp.MustCompile(`(?m)^\|([^|]+)\|([^|]+)\|([^|]+)\|`)
	areaRe  = regexp.MustCompile(`^[A-Za-z_]+$`)
	countRe = regexp.MustCompile(`^\d+$`)
	numRe   = regexp.MustCompile(`\d+`)
	pieceRe = regexp.MustCompile(`[,+]`)
	totalRe = regexp.MustCompile(`(?m)^Total:\s*\**(\d+)\s*cases\**`)
)

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

// checkIndex checks INDEX.md's "Area <-> id band <-> fixture fragment"
// table — per-area case count and band membership, plus the closing
// "Total: N cases" line — against the ids observed on disk. It is a direct
// port of validate.py's INDEX.md section, findings text included.
func checkIndex(indexPath string, areaIDs map[string]map[int]bool, totalOnDisk int) ([]string, error) {
	b, err := os.ReadFile(indexPath)
	if err != nil {
		return nil, err
	}
	text := string(b)

	var findings []string

	// Parse every 3+-column markdown table row "| area | count | band(s) |",
	// ignoring header/separator rows and rows of other tables in the file
	// (their first two cells aren't a bare word + a bare integer).
	indexAreas := map[string]areaClaim{}
	for _, m := range rowRe.FindAllStringSubmatch(text, -1) {
		areaCell := strings.ReplaceAll(strings.TrimSpace(m[1]), "**", "")
		countCell := strings.ReplaceAll(strings.TrimSpace(m[2]), "**", "")
		bandCell := strings.ReplaceAll(strings.TrimSpace(m[3]), "**", "")
		if !areaRe.MatchString(areaCell) || !countRe.MatchString(countCell) {
			continue
		}
		var ranges []idRange
		for _, piece := range pieceRe.Split(bandCell, -1) {
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
			indexAreas[areaCell] = areaClaim{count: count, ranges: ranges}
		}
	}

	if len(indexAreas) == 0 {
		findings = append(findings, "INDEX.md: could not parse the Area <-> id band table (0 rows matched)")
	}

	for _, area := range sortedKeys(areaIDs) {
		ids := areaIDs[area]
		claim, ok := indexAreas[area]
		if !ok {
			findings = append(findings, fmt.Sprintf(
				"INDEX.md: no id-band table row for area %q (%d cases on disk)", area, len(ids)))
			continue
		}
		if len(ids) != claim.count {
			findings = append(findings, fmt.Sprintf(
				"INDEX.md: area %q claims %d cases, %d found on disk", area, claim.count, len(ids)))
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
				"INDEX.md: area %q has ids outside its declared band(s) %v: %v", area, claim.ranges, outOfBand))
		}
	}

	for _, area := range sortedKeys(indexAreas) {
		if _, ok := areaIDs[area]; !ok {
			findings = append(findings, fmt.Sprintf(
				"INDEX.md: area %q is listed in the table but no case on disk has that feature: prefix", area))
		}
	}

	declaredTotal := 0
	for _, claim := range indexAreas {
		declaredTotal += claim.count
	}
	if declaredTotal != totalOnDisk {
		findings = append(findings, fmt.Sprintf(
			"INDEX.md: area counts in the table sum to %d, %d cases found on disk", declaredTotal, totalOnDisk))
	}

	if m := totalRe.FindStringSubmatch(text); m == nil {
		findings = append(findings, "INDEX.md: could not find the 'Total: N cases' summary line")
	} else if n, _ := strconv.Atoi(m[1]); n != totalOnDisk {
		findings = append(findings, fmt.Sprintf(
			"INDEX.md: 'Total:' line claims %d cases, %d found on disk", n, totalOnDisk))
	}

	return findings, nil
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
