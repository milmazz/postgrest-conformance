// Package route decides which PostgREST instance serves a given case: one
// of four shared instances ("bulk", "auth", "multi", "unicode") or a
// per-case config-overlay variant layered on top of one of them.
//
// This mirrors HARNESS.md §2 ("Server configuration"): most cases share one
// of the four fixed instances; a small number declare a `config:` block
// that neither shared instance can honor, so they get a dedicated variant
// instance built by merging their translated config onto the base they
// would otherwise use. CrossCheckHarness compares the routing this package
// derives against the harness's own hand-curated §2.3 table, as a
// consistency check between the two independently-maintained sources.
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
	Base          string         // "bulk" | "auth" | "multi" | "unicode" (http only)
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
// (the default schema, or ones already routed by their own base).
var noInjectSchemas = map[string]bool{
	"":        true,
	"public":  true,
	"test":    true,
	"multi":   true,
	"unicode": true,
}

// harnessVariantIDs are the 33 case ids HARNESS.md §2.3 lists as needing a
// per-case variant instance.
var harnessVariantIDs = []int{
	1139, 1467, 1468, 1469, 1470, 1471, 1472, 1473, 1491, 1493, 1495, 1498,
	1499, 1517, 1518, 1522, 1654, 1677, 1678, 1680, 1682, 1703, 1742, 1758,
	1763, 1764, 11800, 11802, 11803, 11804, 11805, 11807, 11818,
}

// BaseConfigs returns the four base PGRST_* config maps ("bulk", "auth",
// "multi", "unicode"), exclusive of db-uri/ports which are the caller's
// responsibility to fill in.
//
// bulk deliberately omits "openapi" from PGRST_DB_SCHEMAS even though
// HARNESS.md §2.1's db-schemas list names it: the fixture chain
// (fixtures/02_base.sql) creates no schema by that name, and PostgREST
// validates every listed schema exists at boot — with "openapi" included,
// every instance built from bulk/auth/multi/unicode (which all inherit this
// list) failed to become ready ("schema \"openapi\" does not exist"),
// observed as a 100% instance-boot failure across the shared bases. No case
// sends Accept-Profile: openapi (the openapi.sql-derived cases run against
// schema test or openapi_no_comment instead), so dropping it here is
// behavior-neutral for every case; it's recorded as a HARNESS finding
// instead of a fixture edit (fixtures/ is owner-reviewed delta-channel
// work — the runner never writes to it).
//
// bulk also omits PGRST_DB_ANON_ROLE outright, per HARNESS §2.1's stated
// semantics ("no anonymous role... requests run as the connecting database
// user, no role switching"): the runner (run.go) fills it in at boot time
// with the connecting db-uri's own user for the bulk/multi/unicode bases
// (never auth, which keeps its own explicit postgrest_test_anonymous per
// §2.2), rather than baking a role name into this map.
//
// That boot-time injection is deliberately invisible to Route's overlay
// satisfaction check below: `base` there (and pgDefaults's own
// PGRST_DB_ANON_ROLE = "") reflects the map as returned by this function,
// without run.go's later PGRST_DB_ANON_ROLE=<connection user> injection. If
// a non-auth case ever declares db-anon-role in its own config: block, this
// would need revisiting — today none do, every db-anon-role-bearing case is
// auth-based (routed to the "auth" base, which never gets the injection).
func BaseConfigs() map[string]map[string]string {
	bulk := map[string]string{
		"PGRST_DB_SCHEMAS":                  "test,operators,ordering,pagination,representations,mutations,rpc,headers,config,domain_representations,observability,auth,v1,v2,SPECIAL \"@/\\#~_-,تست",
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

	auth := cloneMap(bulk)
	auth["PGRST_DB_ANON_ROLE"] = "postgrest_test_anonymous"
	auth["PGRST_JWT_SECRET"] = "reallyreallyreallyreallyverysafe"
	auth["PGRST_DB_PRE_REQUEST"] = "auth.switch_role"

	multi := cloneMap(bulk)
	multi["PGRST_DB_SCHEMAS"] = `v1,v2,SPECIAL "@/\#~_-`

	unicode := cloneMap(bulk)
	unicode["PGRST_DB_SCHEMAS"] = "تست"

	return map[string]map[string]string{
		"bulk":    bulk,
		"auth":    auth,
		"multi":   multi,
		"unicode": unicode,
	}
}

func cloneMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
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
		p.Base = "bulk"
		p.SafeUpdate = true
		p.InjectProfile = "mutations"
		p.GroupKey = "bulk+safeupdate"
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
		} else {
			p.Base = "bulk"
		}
		if noInjectSchemas[c.Schema] {
			p.InjectProfile = ""
		} else {
			p.InjectProfile = c.Schema
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
// HARNESS.md §2.3's hand-curated 33-id table, returning one finding line
// per disagreement in either direction: a case this package routes to a
// variant instance that §2.3 doesn't list, or a case §2.3 lists that this
// package routes to a shared instance.
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
// one of the four shared ones.
func isVariant(p *Placement) bool {
	return p.GroupKey != p.Base
}
