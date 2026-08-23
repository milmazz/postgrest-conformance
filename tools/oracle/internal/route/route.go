// Package route decides which PostgREST instance serves a given case: one
// of fourteen shared instances — a per-area single-schema base for each of
// "test", "operators", "ordering", "pagination", "representations",
// "mutations", "rpc", "headers", "config", "domain_representations",
// "observability", plus "auth", "multi", "unicode" — or a per-case
// config-overlay variant layered on top of one of them.
//
// This mirrors HARNESS.md §2 ("Server configuration"): the per-area
// single-schema layout of §2.1.1 (adopted for issue #2 — PGRST201
// false-positive ambiguous embeds on a single wide instance; see
// BaseConfigs' doc comment — and documented as the contract by issue #5).
// Most cases share one of the fixed base instances; a small number declare
// a `config:` block that no base can honor, so they get a dedicated
// variant instance built by merging their translated config onto the base
// they would otherwise use. CrossCheckHarness compares the routing this
// package derives against the harness's own hand-curated variant list
// (§2.3's table plus §2.5's safe-update trio), as a consistency check
// between the two independently-maintained sources.
package route

import (
	"fmt"
	"sort"
	"strings"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
)

// Val is a translated config value: either a concrete env value or "clear
// this key" (YAML null — the variant omits the env var).
type Val struct {
	Clear bool
	V     string
}

// Placement is the routing decision for one case.
type Placement struct {
	Kind          string         // "http" | "cli"
	Base          string         // one of BaseConfigs' keys (http only)
	Overlay       map[string]Val // PGRST_* overrides; empty = shared instance
	SafeUpdate    bool           // 1387-1389: db-uri needs safeupdate preloaded
	InjectProfile string
	GroupKey      string // Base when shared; else Base+canonical(Overlay)(+"+safeupdate")
}

// asymJWK is the upstream RS256 public JWK used by AsymmetricJwtSpec.hs,
// verbatim. The sentinel config value "asymmetric_jwk_public_key" expands
// to this string; "asymmetric_jwks_public_key" wraps it as a JWK Set.
const asymJWK = `{"alg":"RS256","e":"AQAB","key_ops":["verify"],"kty":"RSA","n":"0etQ2Tg187jb04MWfpuogYGV75IFrQQBxQaGH75eq_FpbkyoLcEpRUEWSbECP2eeFya2yZ9vIO5ScD-lPmovePk4Aa4SzZ8jdjhmAbNykleRPCxMg0481kz6PQhnHRUv3nF5WP479CnObJKqTVdEagVL66oxnX9VhZG9IZA7k0Th5PfKQwrKGyUeTGczpOjaPqbxlunP73j9AfnAt4XCS8epa-n3WGz1j-wfpr_ys57Aq-zBCfqP67UYzNpeI1AoXsJhD9xSDOzvJgFRvc3vm2wjAW4LEMwi48rCplamOpZToIHEPIaPzpveYQwDnB1HFTR1ove9bpKJsHmi-e2uzQ","use":"sig"}`

// pgDefaults holds PostgREST's own default effective value for a PGRST_*
// key when that key is absent from a base config map. A case's translated
// config entry is "satisfied" by a base (and so needs no overlay) when it
// matches either the base's explicit value or, absent that, this default.
var pgDefaults = map[string]string{
	"PGRST_DB_AGGREGATES_ENABLED":       "false",
	"PGRST_URL_USE_LEGACY_TARGET_NAMES": "true",
	"PGRST_JWT_ROLE_CLAIM_KEY":          "$.role",
	"PGRST_JWT_AUD":                     "",
	"PGRST_JWT_SECRET":                  "",
	"PGRST_JWT_SECRET_IS_BASE64":        "false",
	"PGRST_JWT_CACHE_MAX_ENTRIES":       "10",
	"PGRST_DB_PRE_REQUEST":              "",
	"PGRST_DB_ANON_ROLE":                "",
	"PGRST_CLIENT_ERROR_VERBOSITY":      "verbose",
	"PGRST_OPENAPI_MODE":                "follow-privileges",
	"PGRST_OPENAPI_SECURITY_ACTIVE":     "false",
	"PGRST_DB_ROOT_SPEC":                "",
	"PGRST_DB_MAX_ROWS":                 "",
}

// multiIDs are cases routed to the "multi" base by id even though their
// schema isn't literally "multi" (the headers/profile-routing area).
var multiIDs = map[int]bool{
	1557: true, 1558: true, 1559: true, 1560: true, 1574: true, 1583: true,
}

// noInjectSchemas are schemas that don't get an Accept-Profile injected
// (the default schema, or ones already routed by their own base). Only
// consulted for cases routed to the "auth" base (see Route): every other
// base is now a single-schema instance for the case's own area (or "test"),
// so InjectProfile is unconditionally "" there — the sole exposed schema is
// already the default, nothing to select. The "multi"/"unicode" entries are
// consequently unreachable here too — Route's earlier switch cases always
// intercept those schemas before this map is ever consulted — but are kept
// for documentation/defensiveness.
var noInjectSchemas = map[string]bool{
	"":        true,
	"public":  true,
	"test":    true,
	"multi":   true,
	"unicode": true,
}

// harnessVariantIDs are the 64 case ids HARNESS.md documents as needing a
// per-case variant instance: §2.3's config-driven table (61 ids), plus the
// three §2.5-only safe-update cases 1387–1389, which carry no config: block
// and are selected purely by id.
var harnessVariantIDs = []int{
	1129, 1130, 1131, 1132, 1133, 1139, 1140, 1147, 1148, 1149, 1387, 1388,
	1389, 1466, 1467, 1468, 1469, 1470, 1471, 1472, 1473, 1475, 1476, 1477,
	1491, 1492, 1493, 1494, 1495, 1497, 1498, 1499, 1517, 1518, 1522, 1573,
	1654, 1677, 1678, 1680, 1682, 1700, 1701, 1703, 1742, 1758, 1763, 1764,
	1765, 1766, 1767, 11800, 11801, 11802, 11803, 11804, 11805, 11806, 11807,
	11808, 11815, 11816, 11817, 11818,
}

// areaSchemaLabels are the eleven area schema labels HARNESS.md §2.1.1
// gives their own single-schema instance ("auth", "multi" and "unicode"
// are handled separately — see below), each of which gets its own base
// config from BaseConfigs.
var areaSchemaLabels = []string{
	"test", "operators", "ordering", "pagination", "representations",
	"mutations", "rpc", "headers", "config", "domain_representations",
	"observability",
}

// areaSchemaSet is areaSchemaLabels as a set, for O(1) "is this an area
// label" membership checks (areaBase) instead of a linear scan.
var areaSchemaSet = func() map[string]bool {
	m := make(map[string]bool, len(areaSchemaLabels))
	for _, label := range areaSchemaLabels {
		m[label] = true
	}
	return m
}()

// BaseConfigs returns the fourteen base PGRST_* config maps — one
// single-schema base per areaSchemaLabels entry, plus "auth", "multi", and
// "unicode" — exclusive of db-uri/ports which are the caller's
// responsibility to fill in.
//
// # Per-area single-schema layout (issue #2)
//
// Earlier revisions of HARNESS.md §2.1 described a single "bulk" instance
// whose PGRST_DB_SCHEMAS lists every area schema at once (plus
// auth/v1/v2/SPECIAL/تست), shared by nearly every case. This package
// deliberately does not build that
// instance. Real PostgREST resolves an *unqualified* embed target (e.g.
// `select=id,users(id)`) by scanning relationship candidates across every
// schema named in db-schemas, not just the request's active
// Accept-Profile/Content-Profile — table/column resolution is scoped to the
// active profile, but embed-ambiguity detection is not. Because
// fixtures/06_area_schemas.sql mirrors the entire `test` schema into each
// other pure table/data area schema as views (so each area's own
// Accept-Profile resolves to *something*), a single instance exposing all
// of them at once sees the same `tasks`/`users`/etc. relationship
// independently offered by every mirroring schema — 8 schemas in
// fixtures/06_area_schemas.sql's case — and reports it as an N-way
// ambiguous embed (PGRST201) even for a request against the default `test`
// schema that has no ambiguity at all. This was diagnosed and reproduced by
// hand (manual curl against a bulk-shaped instance) in
// docs/superpowers/notes/2026-08-22-oracle-first-run-triage.md, Finding 1;
// see that document for the full repro and the seven affected cases (1117,
// 1125, 1198, 1199, 1213, 1222, 11415).
//
// Building one instance per area — matching upstream PostgREST's own
// per-area single-schema spec configs — sidesteps this entirely: each
// area's instance has exactly one schema in db-schemas, so there is nothing
// else for embed resolution to scan. This supersedes §2.1's single-shared-
// instance recipe; it's a portability finding against the reference
// harness recipe (a real semantic difference an implementer following §2.1
// literally would hit); HARNESS.md §2.1.1 now documents this layout as the
// contract (issue #5).
//
// auth, multi, and unicode are unaffected by the above — they're built the
// same way as before this change (auth/multi's/unicode's db-schemas lists
// were never area-mirror-only, and no case exercising them exhibited the
// ambiguity) — and are kept byte-for-byte identical to their pre-issue-#2
// values.
//
// Resolved trade-off (case 1824, domain_representations/write/view_post_headers_only,
// issue #9): fixtures/02_base.sql defines test.datarep_todos_computed as a
// VIEW over the test.datarep_todos table (not a base table itself), and
// fixtures/06_area_schemas.sql's domain_representations.datarep_todos_computed
// used to be a pass-through view over *that* view — a two-hop chain. Real
// PostgREST traces a view's column ancestry through pg_depend to find the
// ultimate source table's primary key (needed to compute the Location
// header on insert), and that traversal only considers relations in
// schemas actually listed in db-schemas: with only "domain_representations"
// exposed, the intermediate test.datarep_todos_computed hop was invisible,
// the chain couldn't be completed, no primary key was found, and Location
// was silently omitted (confirmed by hand: booting an otherwise-identical
// instance with PGRST_DB_SCHEMAS="domain_representations,test" restored
// it). Re-adding "test" to this base was never an option — it would
// reintroduce the very cross-schema-relationship exposure the fix above
// removes it to avoid. Instead the fixture generator
// (tools/regen_area_schemas.exs) now mirrors `test` views by copying each
// view's own definition rather than selecting from the view, collapsing
// every area mirror of a view to a single hop over the same base tables —
// so the ancestry chain completes within the area's own db-schemas entry.
//
// auth's (and, before this change, bulk's) PGRST_DB_SCHEMAS deliberately
// omits "openapi" even though HARNESS.md §2.1's db-schemas list names it:
// the fixture chain (fixtures/02_base.sql) creates no schema by that name,
// and PostgREST validates every listed schema exists at boot — with
// "openapi" included, the instance failed to become ready ("schema
// \"openapi\" does not exist"). No case sends Accept-Profile: openapi (the
// openapi.sql-derived cases run against schema test or openapi_no_comment
// instead), so dropping it here is behavior-neutral for every case; it's
// recorded as a HARNESS finding instead of a fixture edit (fixtures/ is
// owner-reviewed delta-channel work — the runner never writes to it).
//
// Every base here also omits PGRST_DB_ANON_ROLE outright except auth, per
// HARNESS §2.1's stated semantics ("no anonymous role... requests run as
// the connecting database user, no role switching"): the runner (run.go)
// fills it in at boot time with the connecting db-uri's own user for every
// base except auth (which keeps its own explicit postgrest_test_anonymous
// per §2.2), rather than baking a role name into this map.
//
// That boot-time injection is deliberately invisible to Route's overlay
// satisfaction check below: `base` there (and pgDefaults's own
// PGRST_DB_ANON_ROLE = "") reflects the map as returned by this function,
// without run.go's later PGRST_DB_ANON_ROLE=<connection user> injection. If
// a non-auth case ever declares db-anon-role in its own config: block, this
// would need revisiting — today none do, every db-anon-role-bearing case is
// auth-based (routed to the "auth" base, which never gets the injection).
func BaseConfigs() map[string]map[string]string {
	// template holds every field shared across all fourteen bases except
	// PGRST_DB_SCHEMAS — every base sets that explicitly itself below, since
	// it's the one field that differs by design (that's the entire point of
	// the per-area split) — and auth's extra keys (added below). Aside from
	// PGRST_DB_SCHEMAS, this is exactly the field set the pre-issue-#2
	// "bulk" base used, verbatim.
	template := map[string]string{
		"PGRST_DB_EXTRA_SEARCH_PATH":        "public",
		"PGRST_DB_POOL":                     "10",
		"PGRST_DB_TX_END":                   "rollback",
		"PGRST_DB_PLAN_ENABLED":             "true",
		"PGRST_DB_CONFIG":                   "false",
		"PGRST_SERVER_CORS_ALLOWED_ORIGINS": "http://example.com, http://example2.com",
		"PGRST_SERVER_TIMING_ENABLED":       "true",
		"PGRST_SERVER_TRACE_HEADER":         "X-Request-Id",
		"PGRST_LOG_LEVEL":                   "error",
		"PGRST_SERVER_HOST":                 "127.0.0.1",
	}

	auth := cloneMap(template)
	// auth's PGRST_DB_SCHEMAS is the same wide list "bulk" used, verbatim —
	// kept byte-for-byte unchanged per BaseConfigs' doc comment. Residual
	// risk: auth still exposes all 8 area mirrors alongside test, so a
	// future auth-routed case that adds an unqualified embed could hit the
	// same false PGRST201 ambiguity this fix removes everywhere else (no
	// case does today); HARNESS.md §2.2 documents this residual (issue #5).
	auth["PGRST_DB_SCHEMAS"] = "test,operators,ordering,pagination,representations,mutations,rpc,headers,config,domain_representations,observability,auth,v1,v2,SPECIAL \"@/\\#~_-,تست"
	auth["PGRST_DB_ANON_ROLE"] = "postgrest_test_anonymous"
	auth["PGRST_JWT_SECRET"] = "reallyreallyreallyreallyverysafe"
	auth["PGRST_DB_PRE_REQUEST"] = "auth.switch_role"

	multi := cloneMap(template)
	multi["PGRST_DB_SCHEMAS"] = `v1,v2,SPECIAL "@/\#~_-`

	unicode := cloneMap(template)
	unicode["PGRST_DB_SCHEMAS"] = "تست"

	out := map[string]map[string]string{
		"auth":    auth,
		"multi":   multi,
		"unicode": unicode,
	}

	for _, label := range areaSchemaLabels {
		m := cloneMap(template)
		m["PGRST_DB_SCHEMAS"] = label
		out[label] = m
	}

	return out
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// areaBase maps a case's schema label to the BaseConfigs key of its
// single-schema instance: "" and "public" (the default/unlabeled schema)
// and "test" itself all map to the "test" base; every other area label maps
// to the identically-named base. A label that isn't in areaSchemaSet is an
// error — every non-auth/multi/unicode schema label actually used by
// cases/*.yaml must have a base here, so a new, unrecognized label likely
// means BaseConfigs needs a new entry (or Route needs a new special case)
// rather than silently misrouting.
func areaBase(schema string) (string, error) {
	switch schema {
	case "", "public":
		return "test", nil
	}
	if areaSchemaSet[schema] {
		return schema, nil
	}
	return "", fmt.Errorf("schema %q has no base config in BaseConfigs — add one or route it explicitly", schema)
}

// envKey maps a case config's kebab-case key to its PGRST_* env var name.
func envKey(kebab string) string {
	return "PGRST_" + strings.ToUpper(strings.ReplaceAll(kebab, "-", "_"))
}

// expandSentinel resolves the two symbolic config values HARNESS.md §2.4
// documents into their literal JWK values; every other string passes
// through unchanged.
func expandSentinel(s string) string {
	switch s {
	case "asymmetric_jwk_public_key":
		return asymJWK
	case "asymmetric_jwks_public_key":
		return `{"keys":[` + asymJWK + `]}`
	default:
		return s
	}
}

// translate converts a case's YAML-typed config map (kebab-case keys) into
// PGRST_* env keys and their translated Val.
func translate(cfg map[string]any) (map[string]Val, error) {
	out := make(map[string]Val, len(cfg))
	for k, v := range cfg {
		key := envKey(k)
		switch t := v.(type) {
		case nil:
			out[key] = Val{Clear: true}
		case bool, int, int64, uint64, float64:
			out[key] = Val{V: fmt.Sprintf("%v", t)}
		case string:
			out[key] = Val{V: expandSentinel(t)}
		default:
			return nil, fmt.Errorf("route: config key %q has unsupported value type %T", k, v)
		}
	}
	return out, nil
}

// Route decides which instance serves c.
func Route(c *cases.Case) (*Placement, error) {
	if c.Request.Kind == "cli" {
		return &Placement{Kind: "cli"}, nil
	}

	p := &Placement{Kind: "http"}

	switch {
	case c.ID == 1387 || c.ID == 1388 || c.ID == 1389:
		if len(c.Config.Keys) > 0 {
			return nil, fmt.Errorf("case %d: safeupdate-routed case carries a config block the router does not translate — extend Route", c.ID)
		}
		// These three are schema:mutations; on the per-area single-schema
		// "mutations" instance no Accept-Profile injection is needed (the
		// sole exposed schema is already the default).
		p.Base = "mutations"
		p.SafeUpdate = true
		p.InjectProfile = ""
		p.GroupKey = "mutations+safeupdate"
		return p, nil

	case c.Schema == "multi" || multiIDs[c.ID]:
		p.Base = "multi"
		p.InjectProfile = ""

	case c.Schema == "unicode":
		p.Base = "unicode"
		p.InjectProfile = ""

	default:
		if c.Schema == "auth" || c.Schema == "openapi" || c.Schema == "openapi_no_comment" ||
			c.Request.Path == "/" || strings.HasPrefix(c.Request.Path, "/?") {
			p.Base = "auth"
			if noInjectSchemas[c.Schema] {
				p.InjectProfile = ""
			} else {
				p.InjectProfile = c.Schema
			}
		} else {
			// Every other case is routed to its own area's single-schema
			// base (or "test" for the default/no-profile schema), which
			// already exposes exactly the one schema the case needs — so,
			// unlike the auth branch above, no Accept-Profile injection is
			// ever needed here.
			base, err := areaBase(c.Schema)
			if err != nil {
				return nil, fmt.Errorf("case %d: %w", c.ID, err)
			}
			p.Base = base
			p.InjectProfile = ""
		}
	}

	base := BaseConfigs()[p.Base]

	overlay := map[string]Val{}
	if c.Config.Present {
		tv, err := translate(c.Config.Keys)
		if err != nil {
			return nil, fmt.Errorf("case %d: %w", c.ID, err)
		}
		for k, v := range tv {
			overlay[k] = v
		}
	}

	// Hard-coded extras (HARNESS.md §2.5), merged after the case's own
	// config so they take precedence on key collision.
	switch c.ID {
	case 1654:
		overlay["PGRST_DB_SCHEMAS"] = Val{V: "openapi_no_comment"}
	case 1764:
		overlay["PGRST_JWT_SECRET"] = Val{Clear: true}
	}

	final := make(map[string]Val, len(overlay))
	for k, v := range overlay {
		eff, ok := base[k]
		if !ok {
			eff, ok = pgDefaults[k]
		}
		if !ok {
			eff = ""
		}
		satisfied := (v.Clear && eff == "") || (!v.Clear && eff == v.V)
		if !satisfied {
			final[k] = v
		}
	}
	p.Overlay = final
	p.GroupKey = groupKey(p.Base, final)

	return p, nil
}

// groupKey computes the canonical instance-sharing key for a base and its
// (already-filtered) overlay: the base name alone when the overlay is
// empty (the case is served by the shared instance), else the base plus a
// deterministic serialization of the overlay so identical overlays land on
// the same variant instance.
func groupKey(base string, overlay map[string]Val) string {
	if len(overlay) == 0 {
		return base
	}

	keys := make([]string, 0, len(overlay))
	for k := range overlay {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := overlay[k]
		if v.Clear {
			parts = append(parts, k+"=∅")
		} else {
			parts = append(parts, k+"="+v.V)
		}
	}
	return base + "|" + strings.Join(parts, ";")
}

// CrossCheckHarness compares the routing decisions in all against
// HARNESS.md's hand-curated variant list (§2.3's table plus §2.5's
// safe-update trio), returning one finding line per disagreement in either
// direction: a case this package routes to a variant instance that
// HARNESS.md doesn't list, or a case HARNESS.md lists that this package
// routes to a shared instance.
func CrossCheckHarness(all map[int]*Placement) []string {
	harnessSet := make(map[int]bool, len(harnessVariantIDs))
	for _, id := range harnessVariantIDs {
		harnessSet[id] = true
	}

	ids := make([]int, 0, len(all))
	for id := range all {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	var findings []string
	for _, id := range ids {
		p := all[id]
		if p.Kind != "http" {
			continue
		}
		if isVariant(p) && !harnessSet[id] {
			findings = append(findings, fmt.Sprintf("case %d: routed to a variant instance but not listed in HARNESS §2.3", id))
		}
	}
	for _, id := range harnessVariantIDs {
		p, ok := all[id]
		if !ok || p.Kind != "http" {
			continue
		}
		if !isVariant(p) {
			findings = append(findings, fmt.Sprintf("case %d: listed in HARNESS §2.3 but routes to a shared instance", id))
		}
	}
	return findings
}

// isVariant reports whether p is served by a dedicated instance rather than
// one of the fourteen shared ones.
func isVariant(p *Placement) bool {
	return p.GroupKey != p.Base
}
