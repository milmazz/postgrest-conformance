package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/assert"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cliexec"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/db"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/fetchbin"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/httpexec"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/instance"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/report"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/route"
)

func init() {
	dispatch["run"] = cmdRun
}

// sharedGroupOrder is the fixed boot order for the four shared instances,
// each one skipped when it has no selected cases.
var sharedGroupOrder = []string{"bulk", "auth", "multi", "unicode"}

// cmdRun executes every selected case against a real PostgREST binary: CLI
// cases first (id order), then HTTP cases grouped by their routing
// (route.Placement.GroupKey), booting one instance per group. It writes a
// JSON report and a human-readable summary, and returns a non-nil error iff
// any selected case failed (or the run was interrupted).
func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	casesFlag := fs.String("cases", "", "comma-separated case ids to run (default: all)")
	areasFlag := fs.String("areas", "", "comma-separated area names to run (default: all)")
	binFlag := fs.String("bin", "", "path to the PostgREST binary (default: fetched per PIN)")
	dbFlag := fs.String("db", defaultDBName, "target database name")
	reportFlag := fs.String("report", "report.json", "path to write the JSON report")
	skipCLI := fs.Bool("skip-cli", false, "skip request.kind: cli cases")
	skipHTTP := fs.Bool("skip-http", false, "skip HTTP cases")
	if err := fs.Parse(args); err != nil {
		return err
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}

	bin := *binFlag
	if bin == "" {
		bin, err = fetchbin.Fetch(repoRoot)
		if err != nil {
			return err
		}
	}

	pg := db.FromEnv()
	dbname := *dbFlag

	all, err := cases.LoadAll(filepath.Join(repoRoot, "cases"))
	if err != nil {
		return err
	}

	idSet, err := parseIDSet(*casesFlag)
	if err != nil {
		return err
	}
	areaSet := parseStringSet(*areasFlag)
	selected := filterCases(all, idSet, areaSet)
	if err := checkSelection(effectiveSelection(selected, *skipCLI, *skipHTTP)); err != nil {
		return err
	}

	placements := make(map[int]*route.Placement, len(all))
	for _, c := range all {
		p, err := route.Route(c)
		if err != nil {
			return err
		}
		placements[c.ID] = p
	}
	findings := route.CrossCheckHarness(placements)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	reg := newInstanceRegistry()
	go func() {
		<-ctx.Done()
		reg.stopAll()
	}()

	var results []report.CaseResult
	ranHTTP := false

	if !*skipCLI {
		for _, c := range selected {
			if ctx.Err() != nil {
				break
			}
			if c.Request.Kind != "cli" {
				continue
			}
			failures := runCLICase(c, bin, &pg, dbname)
			results = append(results, toResult(c, "cli", failures))
		}
	}

	if !*skipHTTP {
		uri := pg.URI(dbname)
		bases := route.BaseConfigs()
		shared := map[string]*instance.Instance{}
		groups := groupsInOrder(placements, selected)
		ranHTTP = len(groups) > 0

		for _, g := range groups {
			if ctx.Err() != nil {
				break
			}

			base, ok := bases[g.base]
			if !ok {
				msg := fmt.Sprintf("route: unknown base %q", g.base)
				for _, c := range g.cases {
					results = append(results, toResult(c, g.key, []string{msg}))
				}
				continue
			}
			base = withConnUserAnon(base, g.base, pg.User)

			inst, startErr := instance.Start(bin, base, g.overlay, uri, g.safeUpdate)
			if startErr != nil {
				msg := startErr.Error()
				for _, c := range g.cases {
					results = append(results, toResult(c, g.key, []string{msg}))
				}
				continue
			}
			reg.add(inst)

			for _, c := range g.cases {
				if ctx.Err() != nil {
					break
				}
				injectProfile := placements[c.ID].InjectProfile
				failures := runHTTPCase(c, inst.Port, injectProfile)
				results = append(results, toResult(c, g.key, failures))
			}

			if g.shared {
				// Shared bases stay up until every group has run; stopped
				// together below.
				shared[g.key] = inst
			} else {
				inst.Stop()
				reg.remove(inst)
			}
		}

		for _, inst := range shared {
			inst.Stop()
			reg.remove(inst)
		}
	}

	// Cleanup: stop anything still registered (belt-and-suspenders — normal
	// flow above already stops every instance it started), then best-effort
	// drop the fixture-chain role a db-config CLI case may have created.
	reg.stopAll()
	_ = pg.Psql("postgres", nil, "-c", "DROP ROLE IF EXISTS db_config_authenticator")

	// Belt-and-suspenders: effectiveSelection above already guarantees a
	// non-empty set before the run loops start, so this should be
	// unreachable in practice — but if some future change to the loops
	// above lets the effective set run to completion with zero results
	// recorded, fail loudly here rather than print a vacuous "TOTAL 0/0"
	// summary and exit 0.
	if len(results) == 0 {
		return errNoCasesSelected
	}

	findings = findingsForRun(findings, ranHTTP)

	if err := report.WriteJSON(*reportFlag, results, findings); err != nil {
		return err
	}
	allPass := report.Summary(os.Stdout, results, findings)

	if ctx.Err() != nil {
		return fmt.Errorf("interrupted")
	}
	if !allPass {
		nFailed := 0
		for _, r := range results {
			if !r.Pass {
				nFailed++
			}
		}
		return fmt.Errorf("%d case(s) failed", nFailed)
	}
	return nil
}

// runCLICase executes one request.kind: cli case, converting any executor
// error — or a panic anywhere in the executor/assertion chain, e.g. from a
// malformed config.file value — into a case failure rather than letting it
// escape.
func runCLICase(c *cases.Case, bin string, pg *db.PGEnv, dbname string) (failures []string) {
	defer func() {
		if r := recover(); r != nil {
			failures = []string{fmt.Sprintf("panic: %v", r)}
		}
	}()

	res, err := cliexec.Run(c, bin, pg, dbname)
	if err != nil {
		return []string{err.Error()}
	}
	return assert.CheckCLI(c, res)
}

// runHTTPCase executes one HTTP case against the instance listening on
// port, converting any spec-build/transport error — or a panic anywhere in
// that chain — into a case failure rather than letting it escape.
func runHTTPCase(c *cases.Case, port int, injectProfile string) (failures []string) {
	defer func() {
		if r := recover(); r != nil {
			failures = []string{fmt.Sprintf("panic: %v", r)}
		}
	}()

	spec, err := httpexec.BuildSpec(c, injectProfile)
	if err != nil {
		return []string{err.Error()}
	}
	resp, err := httpexec.Do(port, spec)
	if err != nil {
		return []string{err.Error()}
	}
	return assert.CheckHTTP(c, resp)
}

// withConnUserAnon returns base with PGRST_DB_ANON_ROLE set to connUser (the
// runner's own db-uri connection user, from db.FromEnv), for every base
// except "auth" — which keeps its own explicit postgrest_test_anonymous per
// HARNESS.md §2.2. This implements §2.1's stated semantics for the other
// three shared bases ("no anonymous role... requests run as the connecting
// database user, no role switching") without baking a role name into
// route.BaseConfigs: PostgREST requires *some* db-anon-role to serve
// unauthenticated requests at all (its absence produced a 100% "401
// Anonymous access is disabled" failure across every anonymous
// bulk/multi/unicode request, observed during smoke-testing), and setting
// it to the connecting user itself is the faithful equivalent of "no role
// switching" rather than importing an unrelated role (e.g.
// postgrest_test_anonymous) whose grants don't cover the bulk/multi/unicode
// area-mirror schemas anyway.
//
// base is never mutated in place — it may be the same map object shared by
// every group routed to the same base (including variants layered on top),
// so a fresh copy is returned instead.
func withConnUserAnon(base map[string]string, baseName, connUser string) map[string]string {
	if baseName == "auth" {
		return base
	}
	out := make(map[string]string, len(base)+1)
	for k, v := range base {
		out[k] = v
	}
	out["PGRST_DB_ANON_ROLE"] = connUser
	return out
}

// toResult builds a report.CaseResult from a case's failure list (nil/empty
// means pass).
func toResult(c *cases.Case, placement string, failures []string) report.CaseResult {
	return report.CaseResult{
		ID:        c.ID,
		Feature:   c.Feature,
		Area:      c.Area,
		Placement: placement,
		Pass:      len(failures) == 0,
		Failures:  failures,
		Source:    c.Source,
	}
}

// parseIDSet parses a comma-separated list of case ids. An empty string
// returns a nil set, meaning "no id filter".
func parseIDSet(s string) (map[int]bool, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	out := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid -cases entry %q: %w", part, err)
		}
		out[id] = true
	}
	return out, nil
}

// parseStringSet parses a comma-separated list of area names. An empty
// string returns a nil set, meaning "no area filter".
func parseStringSet(s string) map[string]bool {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	out := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out[part] = true
	}
	return out
}

// filterCases narrows all to the cases selected by ids/areas, both nil
// meaning "everything". When both are given, a case must satisfy both
// (they narrow one another rather than union).
func filterCases(all []*cases.Case, ids map[int]bool, areas map[string]bool) []*cases.Case {
	if ids == nil && areas == nil {
		return all
	}
	out := make([]*cases.Case, 0, len(all))
	for _, c := range all {
		if ids != nil && !ids[c.ID] {
			continue
		}
		if areas != nil && !areas[c.Area] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// errNoCasesSelected is returned both by checkSelection, run on the
// post-filter/post-skip effective set before anything executes, and as a
// belt-and-suspenders check after the run loops in case some future change
// still lets the effective set produce zero results.
var errNoCasesSelected = errors.New("no cases selected (filters/skips matched nothing)")

// checkSelection returns an error when selected is empty, so an over-narrow
// -cases/-areas filter (a typo'd id, a misspelled area, or two filters that
// don't overlap) — or a -skip-cli/-skip-http flag that excludes every case
// the id/area filters selected (e.g. `-cases 1705 -skip-cli` where 1705 is
// cli-only) — is reported as a hard failure instead of silently printing a
// vacuous "TOTAL 0/0" summary and exiting 0. Callers must pass the effective
// selection (see effectiveSelection), not the raw id/area-filtered one.
func checkSelection(selected []*cases.Case) error {
	if len(selected) == 0 {
		return errNoCasesSelected
	}
	return nil
}

// effectiveSelection narrows selected to the cases that will actually be
// executed once -skip-cli/-skip-http are applied, mirroring the filtering
// the run loops themselves perform (the CLI loop only ever runs
// Request.Kind == "cli" cases and is skipped outright when skipCLI; the
// HTTP loop, via groupsInOrder, only ever sees placements with
// Kind == "http" and is skipped outright when skipHTTP). It exists so
// checkSelection can validate the set that will actually run, closing the
// gap where e.g. a CLI-only -cases selection combined with -skip-cli would
// otherwise pass the id/area filter and then execute zero cases.
func effectiveSelection(selected []*cases.Case, skipCLI, skipHTTP bool) []*cases.Case {
	if !skipCLI && !skipHTTP {
		return selected
	}
	out := make([]*cases.Case, 0, len(selected))
	for _, c := range selected {
		if c.Request.Kind == "cli" {
			if !skipCLI {
				out = append(out, c)
			}
			continue
		}
		if !skipHTTP {
			out = append(out, c)
		}
	}
	return out
}

// httpDeviationFindings are the two boot-time HARNESS deviations that only
// apply once the run actually starts a PostgREST instance: see BaseConfigs'
// doc comment (the "openapi" schema dropped from db-schemas) and
// withConnUserAnon's doc comment (bulk/multi/unicode booted with
// db-anon-role set to the connecting database user). They live only in code
// comments otherwise, so this surfaces them in the run's own findings
// output rather than requiring a reader to go find the source.
func httpDeviationFindings() []string {
	return []string{
		"HARNESS deviation: §2.1 db-schemas entry 'openapi' dropped — schema does not exist in the fixture chain (PostgREST refuses to boot); no case uses that profile",
		"HARNESS deviation: §2.1 bulk/multi/unicode run with db-anon-role=<connection user> — real PostgREST rejects JWT-less requests without an anon role; matches §2.1's 'requests run as the connecting database user'",
	}
}

// findingsForRun appends httpDeviationFindings to base when ranHTTP is true
// (the run started at least one HTTP case — i.e. -skip-http wasn't passed
// and the selection contained at least one HTTP case), leaving base
// untouched otherwise since neither deviation is exercised by a CLI-only
// run.
func findingsForRun(base []string, ranHTTP bool) []string {
	if !ranHTTP {
		return base
	}
	return append(base, httpDeviationFindings()...)
}

// httpGroup is one instance's worth of selected HTTP cases: everything
// sharing a route.Placement.GroupKey, which is exactly what one PostgREST
// process configuration serves.
type httpGroup struct {
	key        string
	base       string
	overlay    map[string]route.Val
	safeUpdate bool
	shared     bool // one of the four fixed bulk/auth/multi/unicode bases
	cases      []*cases.Case
}

// groupsInOrder buckets selected's HTTP cases (those routed to placements
// with Kind == "http") by GroupKey and orders the groups: the shared bases
// bulk, auth, multi, unicode first — each included only if it has at least
// one selected case — then every other (variant) group sorted by its
// smallest case id. Cases within a group are in id order because selected
// itself is (cases.LoadAll sorts by id, and filterCases preserves order).
func groupsInOrder(placements map[int]*route.Placement, selected []*cases.Case) []*httpGroup {
	byKey := map[string]*httpGroup{}
	var keyOrder []string

	for _, c := range selected {
		p := placements[c.ID]
		if p == nil || p.Kind != "http" {
			continue
		}
		g, ok := byKey[p.GroupKey]
		if !ok {
			g = &httpGroup{
				key:        p.GroupKey,
				base:       p.Base,
				overlay:    p.Overlay,
				safeUpdate: p.SafeUpdate,
				shared:     isSharedGroupKey(p.GroupKey),
			}
			byKey[p.GroupKey] = g
			keyOrder = append(keyOrder, p.GroupKey)
		}
		g.cases = append(g.cases, c)
	}

	var shared, variant []*httpGroup
	for _, k := range keyOrder {
		g := byKey[k]
		if g.shared {
			shared = append(shared, g)
		} else {
			variant = append(variant, g)
		}
	}

	sort.Slice(shared, func(i, j int) bool {
		return sharedGroupRank(shared[i].key) < sharedGroupRank(shared[j].key)
	})
	sort.Slice(variant, func(i, j int) bool {
		return minCaseID(variant[i]) < minCaseID(variant[j])
	})

	return append(shared, variant...)
}

// isSharedGroupKey reports whether key names one of the four fixed shared
// instances outright (as opposed to a per-case variant built on top of one,
// whose GroupKey is always base+overlay — never just the bare base name).
func isSharedGroupKey(key string) bool {
	for _, s := range sharedGroupOrder {
		if key == s {
			return true
		}
	}
	return false
}

// sharedGroupRank returns key's position in sharedGroupOrder, used to sort
// the shared groups into their fixed boot order.
func sharedGroupRank(key string) int {
	for i, s := range sharedGroupOrder {
		if s == key {
			return i
		}
	}
	return len(sharedGroupOrder)
}

// minCaseID returns the smallest case id in g, used to order variant groups.
func minCaseID(g *httpGroup) int {
	min := g.cases[0].ID
	for _, c := range g.cases[1:] {
		if c.ID < min {
			min = c.ID
		}
	}
	return min
}

// instanceRegistry tracks the PostgREST instances currently running so a
// SIGINT/SIGTERM can stop all of them promptly, even mid-group. instance.Stop
// is idempotent, so an instance stopped here and again through the normal
// per-group/end-of-run cleanup is safe.
type instanceRegistry struct {
	mu  sync.Mutex
	set map[*instance.Instance]bool
}

func newInstanceRegistry() *instanceRegistry {
	return &instanceRegistry{set: map[*instance.Instance]bool{}}
}

func (r *instanceRegistry) add(i *instance.Instance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.set[i] = true
}

func (r *instanceRegistry) remove(i *instance.Instance) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.set, i)
}

func (r *instanceRegistry) stopAll() {
	r.mu.Lock()
	instances := make([]*instance.Instance, 0, len(r.set))
	for i := range r.set {
		instances = append(instances, i)
	}
	r.set = map[*instance.Instance]bool{}
	r.mu.Unlock()

	for _, i := range instances {
		i.Stop()
	}
}
