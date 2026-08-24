package validate

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The CLI-set claims are the tree's other machine-checkable contract: how
// many `request.kind: cli` cases exist, which ids they are, and how the
// `request.flag` values distribute. Unlike the area tables, they live in
// running prose across four documents, so nothing structural marks them —
// which is how five of them went stale in one pass while `oracle validate`
// stayed green (issue #21).
//
// Each claim below is anchored on a distinctive present-tense phrasing and
// MUST match: a site that is reworded past its anchor reports "could not
// find" rather than silently dropping out of the check. That is the whole
// point — a guard that quietly stops guarding is worse than none, because
// the documents keep looking verified.
//
// The anchors deliberately do not match COVERAGE.md's dated
// "Re-verified an Nth time at the N-case state" snapshots, which restate an
// older CLI set on purpose and are history, not live claims.

// cliClaim is one document's statement about the CLI set. Named capture
// groups select what is checked: "count" (an integer), "ids" (a band
// expression) and the three flag groups.
type cliClaim struct {
	doc  string
	what string // human-readable name of the site, for finding text
	re   *regexp.Regexp
}

var cliClaims = []cliClaim{
	{
		doc:  "INDEX.md",
		what: "the Case file shapes CLI paragraph",
		re: regexp.MustCompile(
			`rather than an HTTP status — \*\*(?P<count>\d+)\*\* cases, ids\s+\*\*(?P<ids>[^*]+)\*\*`),
	},
	{
		doc:  "INDEX.md",
		what: "the Case file shapes flag histogram",
		re: regexp.MustCompile(
			`(?s)"--dump-config"` + "`" + ` \(\*\*(?P<dumpconfig>\d+)\*\* cases\).*?` +
				`"--ready"` + "`" + ` \(\*\*(?P<ready>\d+)\*\*.*?` +
				`"--example"` + "`" + ` \(\*\*(?P<example>\d+)\*\*`),
	},
	{
		doc:  "HARNESS.md",
		what: "the §4 CLI flag vocabulary",
		re: regexp.MustCompile(
			`(?s)` + "`" + `--dump-config` + "`" + ` \((?P<dumpconfig>\d+) cases\), ` +
				"`" + `--ready` + "`" + ` \((?P<ready>\d+)\) and ` +
				"`" + `--example` + "`" + ` \((?P<example>\d+)\)`),
	},
	{
		doc:  "spec/README.md",
		what: "the CLI case-shape bullet",
		re: regexp.MustCompile(
			`(?s)\*\*CLI\*\* \([^)]*?(?P<count>\d+) cases —.*?ids \*\*(?P<ids>[^*]+)\*\*`),
	},
	{
		doc:  "spec/README.md",
		what: "the CLI case-shape flag list",
		re: regexp.MustCompile(
			`(?s)"--dump-config"` + "`" + ` \((?P<dumpconfig>\d+) cases\), ` +
				"`" + `"--ready"` + "`" + ` \((?P<ready>\d+):.*?` +
				`"--example"` + "`" + ` \((?P<example>\d+):`),
	},
	{
		doc:  "COVERAGE.md",
		what: "the cli docs-page mapping row",
		re: regexp.MustCompile(
			`\|\s*` + "`" + `cli` + "`" + ` \(CLI\) \|(?P<ids>[^|]+?)\(all (?P<count>\d+) ` +
				"`" + `request\.kind: cli` + "`" + ` cases\)\s*\|`),
	},
	{
		doc:  "COVERAGE.md",
		what: "the cli scope-decision bullet",
		re: regexp.MustCompile(
			`(?s)the CLI set is now\s+\*\*(?P<ids>[^*]+?)\((?P<count>\d+) cases\)\*\*`),
	},
}

var (
	// cliPieceRe splits a band expression on the separators the documents
	// use between runs: ",", "+" and the word "plus".
	cliPieceRe = regexp.MustCompile(`,|\+|\bplus\b`)
	// cliParenRe strips parentheticals, whose digits are commentary on the
	// range ("(new 2026-08-24)"), not bounds.
	cliParenRe = regexp.MustCompile(`\([^)]*\)`)
	cliNumRe   = regexp.MustCompile(`\d+`)
)

// cliFacts is what the tree actually contains.
type cliFacts struct {
	ids   map[int]bool
	flags map[string]int // request.flag -> count
}

// flagGroups maps a claim's capture-group name to the request.flag value it
// counts.
var flagGroups = map[string]string{
	"dumpconfig": "--dump-config",
	"ready":      "--ready",
	"example":    "--example",
}

// checkCLIShape checks every document's CLI-set claim against the cases on
// disk.
func checkCLIShape(root string, facts cliFacts) ([]string, error) {
	// A tree with no `kind: cli` cases has no CLI set to claim, so there is
	// nothing to guard. This is a fixture shape, not the repository: the
	// real tree has had CLI cases since the config band was written, and
	// deleting all of them would fail the area tables long before this.
	if len(facts.ids) == 0 {
		return nil, nil
	}

	var findings []string

	// Read each document once, in a stable order.
	texts := map[string]string{}
	for _, c := range cliClaims {
		if _, ok := texts[c.doc]; ok {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(c.doc)))
		if err != nil {
			return nil, err
		}
		texts[c.doc] = string(b)
	}

	for _, c := range cliClaims {
		m := c.re.FindStringSubmatch(texts[c.doc])
		if m == nil {
			findings = append(findings, fmt.Sprintf(
				"%s: could not find %s — the CLI-set claim it states is no longer "+
					"machine-checked; re-anchor checkCLIShape or restore the wording",
				c.doc, c.what))
			continue
		}
		findings = append(findings, c.verify(m, facts)...)
	}
	return findings, nil
}

// verify compares one matched claim against the tree.
func (c cliClaim) verify(m []string, facts cliFacts) []string {
	var findings []string
	for i, name := range c.re.SubexpNames() {
		if name == "" {
			continue
		}
		got := strings.TrimSpace(m[i])
		switch name {
		case "count":
			want := len(facts.ids)
			n, err := strconv.Atoi(got)
			if err != nil || n != want {
				findings = append(findings, fmt.Sprintf(
					"%s: %s says %s CLI cases, the tree has %d",
					c.doc, c.what, got, want))
			}
		case "ids":
			claimed := parseCLIBand(got)
			if diff := setDiff(claimed, facts.ids); diff != "" {
				findings = append(findings, fmt.Sprintf(
					"%s: %s claims CLI ids %q, which %s",
					c.doc, c.what, got, diff))
			}
		default:
			flag, ok := flagGroups[name]
			if !ok {
				continue
			}
			want := facts.flags[flag]
			n, err := strconv.Atoi(got)
			if err != nil || n != want {
				findings = append(findings, fmt.Sprintf(
					"%s: %s says %s %s cases, the tree has %d",
					c.doc, c.what, got, flag, want))
			}
		}
	}
	return findings
}

// parseCLIBand expands a band expression ("102 plus 104-105", "102 + 104,
// 105") into the id set it denotes.
func parseCLIBand(s string) map[int]bool {
	ids := map[int]bool{}
	for _, piece := range cliPieceRe.Split(cliParenRe.ReplaceAllString(s, ""), -1) {
		nums := cliNumRe.FindAllString(piece, -1)
		switch len(nums) {
		case 2:
			lo, _ := strconv.Atoi(nums[0])
			hi, _ := strconv.Atoi(nums[1])
			for id := lo; id <= hi; id++ {
				ids[id] = true
			}
		case 1:
			n, _ := strconv.Atoi(nums[0])
			ids[n] = true
		}
	}
	return ids
}

// setDiff describes how claimed differs from actual, or "" if they agree.
func setDiff(claimed, actual map[int]bool) string {
	var missing, extra []int
	for id := range actual {
		if !claimed[id] {
			missing = append(missing, id)
		}
	}
	for id := range claimed {
		if !actual[id] {
			extra = append(extra, id)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	sort.Ints(missing)
	sort.Ints(extra)
	var parts []string
	if len(missing) > 0 {
		parts = append(parts, "omits CLI cases on disk: "+joinInts(missing))
	}
	if len(extra) > 0 {
		parts = append(parts, "names ids that are not CLI cases: "+joinInts(extra))
	}
	return strings.Join(parts, "; ")
}

func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ", ")
}
