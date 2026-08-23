// Package cases loads and validates the repository's PostgREST conformance
// test cases (cases/*.yaml) into an in-memory representation the oracle
// runner can execute against a real PostgREST binary.
//
// Loading is intentionally strict: any `expect:` key outside the allowed
// set is a load error (fail loudly per HARNESS §4), and body-field presence
// (request or expected response) is tracked explicitly so that an assertion
// of "no body" (e.g. `body_exact: null`) is distinguishable from the key
// being absent altogether.
package cases

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// JWT instructs the runner to mint a signed JWT and send it as the bearer
// token for the request.
type JWT struct {
	SignWith string
	Payload  map[string]any
}

// Request is either an HTTP request (Kind == "") or a CLI invocation
// (Kind == "cli").
type Request struct {
	Kind    string // "" (HTTP) or "cli"
	Method  string
	Path    string
	Headers map[string]string
	JWT     *JWT

	Body    any
	HasBody bool

	BodyJSON    any
	HasBodyJSON bool

	BodyRaw    string
	HasBodyRaw bool

	Flag string // CLI only
}

// Config is the optional PostgREST configuration a case requires. Present
// is false when the case has no `config:` key at all, which is distinct
// from a `config:` key with no extra keys beyond the reserved ones.
type Config struct {
	Present          bool
	Keys             map[string]any    // HTTP config keys (kebab-case); may be empty
	Env              map[string]string // CLI
	File             map[string]any    // CLI
	PreconditionsSQL []string          // CLI
}

// Case is one black-box conformance scenario loaded from a cases/*.yaml
// file.
type Case struct {
	ID      int
	Feature string
	Area    string // first '/'-segment of Feature
	Schema  string
	Request Request
	Config  Config
	Expect  map[string]any
	Notes   string
	Source  string
	Path    string // source file path
}

// allowedExpectKeys is the closed set of keys `expect:` may contain. Any
// other key is a load error.
var allowedExpectKeys = map[string]bool{
	"status":                  true,
	"status_text":             true,
	"headers":                 true,
	"headers_present":         true,
	"headers_absent":          true,
	"headers_match":           true,
	"headers_absent_in_value": true,
	"headers_no_blank":        true,
	"body_exact":              true,
	"body_json":               true,
	"body_contains":           true,
	"body_raw":                true,
	"body_jsonpath":           true,
	"exit_code":               true,
	"dump_contains":           true,
	"stderr_contains":         true,
	"dump_reparse_stable":     true,
}

// requiredKeys mirrors case.schema.json's top-level `required` list.
var requiredKeys = []string{"id", "feature", "request", "schema", "expect", "source"}

// Load reads, parses, and validates a single case file.
func Load(path string) (*Case, error) {
	docAny, err := rawYAML(path)
	if err != nil {
		return nil, err
	}

	root, ok := docAny.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: top-level YAML document must be a mapping", path)
	}

	for _, key := range requiredKeys {
		if _, ok := root[key]; !ok {
			return nil, fmt.Errorf("%s: missing required key %q", path, key)
		}
	}

	id, err := toInt(root["id"])
	if err != nil {
		return nil, fmt.Errorf("%s: field id: %w", path, err)
	}

	feature, ok := root["feature"].(string)
	if !ok {
		return nil, fmt.Errorf("%s: field feature must be a string", path)
	}

	schemaName, ok := root["schema"].(string)
	if !ok {
		return nil, fmt.Errorf("%s: field schema must be a string", path)
	}

	source, ok := root["source"].(string)
	if !ok {
		return nil, fmt.Errorf("%s: field source must be a string", path)
	}

	notes := ""
	if v, ok := root["notes"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%s: field notes must be a string", path)
		}
		notes = s
	}

	reqRaw, ok := root["request"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: field request must be a mapping", path)
	}
	request, err := parseRequest(path, reqRaw)
	if err != nil {
		return nil, err
	}

	expectRaw, ok := root["expect"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: field expect must be a mapping", path)
	}
	for k := range expectRaw {
		if !allowedExpectKeys[k] {
			return nil, fmt.Errorf("%s: unknown expect key %q", path, k)
		}
	}

	cfgVal, cfgPresent := root["config"]
	cfg, err := parseConfig(path, cfgVal, cfgPresent)
	if err != nil {
		return nil, err
	}

	area := feature
	if i := strings.IndexByte(feature, '/'); i >= 0 {
		area = feature[:i]
	}

	// normalizeYAML guards against a future non-string-keyed YAML mapping
	// anywhere under expect: (e.g. an unquoted integer-looking key in a
	// body_exact fixture) reaching jsonval.DeepEqual as a map[any]any,
	// which it doesn't recognize. normalizeYAML on a map[string]any input
	// always returns a map[string]any, so this type assertion cannot fail.
	expect := normalizeYAML(expectRaw).(map[string]any)

	return &Case{
		ID:      id,
		Feature: feature,
		Area:    area,
		Schema:  schemaName,
		Request: request,
		Config:  cfg,
		Expect:  expect,
		Notes:   notes,
		Source:  source,
		Path:    path,
	}, nil
}

// LoadAll loads every *.yaml file directly under dir, returning them sorted
// by ID. Duplicate IDs are a load error.
func LoadAll(dir string) ([]*Case, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}

	out := make([]*Case, 0, len(files))
	seenBy := make(map[int]string, len(files))
	for _, f := range files {
		c, err := Load(f)
		if err != nil {
			return nil, err
		}
		if prev, dup := seenBy[c.ID]; dup {
			return nil, fmt.Errorf("duplicate case id %d: %s and %s", c.ID, prev, f)
		}
		seenBy[c.ID] = f
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func parseRequest(path string, m map[string]any) (Request, error) {
	var req Request

	kind := ""
	if kindRaw, hasKind := m["kind"]; hasKind {
		s, ok := kindRaw.(string)
		if !ok {
			return req, fmt.Errorf("%s: request.kind must be a string", path)
		}
		switch s {
		case "", "http":
			kind = ""
		case "cli":
			kind = "cli"
		default:
			return req, fmt.Errorf("%s: request.kind must be %q or %q, got %q", path, "", "cli", s)
		}
	}
	req.Kind = kind

	if v, ok := m["method"]; ok {
		s, ok := v.(string)
		if !ok {
			return req, fmt.Errorf("%s: request.method must be a string", path)
		}
		req.Method = s
	}
	if v, ok := m["path"]; ok {
		s, ok := v.(string)
		if !ok {
			return req, fmt.Errorf("%s: request.path must be a string", path)
		}
		req.Path = s
	}
	if v, ok := m["flag"]; ok {
		s, ok := v.(string)
		if !ok {
			return req, fmt.Errorf("%s: request.flag must be a string", path)
		}
		req.Flag = s
	}

	if kind == "cli" {
		if req.Flag == "" {
			return req, fmt.Errorf("%s: request.flag is required when kind is cli", path)
		}
	} else {
		if req.Method == "" || req.Path == "" {
			return req, fmt.Errorf("%s: request.method and request.path are required for HTTP requests", path)
		}
	}

	if hdrRaw, ok := m["headers"]; ok {
		hm, ok := hdrRaw.(map[string]any)
		if !ok {
			return req, fmt.Errorf("%s: request.headers must be a mapping", path)
		}
		req.Headers = make(map[string]string, len(hm))
		for k, v := range hm {
			s, err := scalarString(v)
			if err != nil {
				return req, fmt.Errorf("%s: request.headers.%s: %w", path, k, err)
			}
			req.Headers[k] = s
		}
	}

	if jwtRaw, ok := m["jwt"]; ok {
		jm, ok := jwtRaw.(map[string]any)
		if !ok {
			return req, fmt.Errorf("%s: request.jwt must be a mapping", path)
		}
		payloadRaw, ok := jm["payload"]
		if !ok {
			return req, fmt.Errorf("%s: request.jwt.payload is required", path)
		}
		payload, ok := payloadRaw.(map[string]any)
		if !ok {
			return req, fmt.Errorf("%s: request.jwt.payload must be a mapping", path)
		}
		jwt := &JWT{Payload: payload}
		if sw, ok := jm["sign_with"]; ok {
			s, ok := sw.(string)
			if !ok {
				return req, fmt.Errorf("%s: request.jwt.sign_with must be a string", path)
			}
			jwt.SignWith = s
		}
		req.JWT = jwt
	}

	if v, ok := m["body"]; ok {
		req.Body = v
		req.HasBody = true
	}
	if v, ok := m["body_json"]; ok {
		req.BodyJSON = v
		req.HasBodyJSON = true
	}
	if v, ok := m["body_raw"]; ok {
		s, ok := v.(string)
		if !ok {
			return req, fmt.Errorf("%s: request.body_raw must be a string", path)
		}
		req.BodyRaw = s
		req.HasBodyRaw = true
	}

	return req, nil
}

func parseConfig(path string, v any, present bool) (Config, error) {
	if !present {
		return Config{}, nil
	}

	m, ok := v.(map[string]any)
	if !ok {
		return Config{}, fmt.Errorf("%s: field config must be a mapping", path)
	}

	cfg := Config{Present: true, Keys: map[string]any{}}
	for k, vv := range m {
		switch k {
		case "env":
			envRaw, ok := vv.(map[string]any)
			if !ok {
				return Config{}, fmt.Errorf("%s: config.env must be a mapping", path)
			}
			cfg.Env = make(map[string]string, len(envRaw))
			for ek, ev := range envRaw {
				s, err := scalarString(ev)
				if err != nil {
					return Config{}, fmt.Errorf("%s: config.env.%s: %w", path, ek, err)
				}
				cfg.Env[ek] = s
			}
		case "file":
			fileRaw, ok := vv.(map[string]any)
			if !ok {
				return Config{}, fmt.Errorf("%s: config.file must be a mapping", path)
			}
			cfg.File = fileRaw
		case "preconditions_sql":
			listRaw, ok := vv.([]any)
			if !ok {
				return Config{}, fmt.Errorf("%s: config.preconditions_sql must be a list", path)
			}
			sql := make([]string, 0, len(listRaw))
			for _, item := range listRaw {
				s, ok := item.(string)
				if !ok {
					return Config{}, fmt.Errorf("%s: config.preconditions_sql items must be strings", path)
				}
				sql = append(sql, s)
			}
			cfg.PreconditionsSQL = sql
		default:
			cfg.Keys[k] = vv
		}
	}
	return cfg, nil
}

// scalarString converts a YAML scalar to its literal string form. Strings
// pass through unchanged; numbers and booleans are formatted with their
// default Go representation; anything else (a mapping or sequence) is an
// error since it has no scalar string form.
func scalarString(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case int, int64, uint64, float64, bool:
		return fmt.Sprintf("%v", t), nil
	default:
		return "", fmt.Errorf("cannot convert %T to string", v)
	}
}

// toInt converts a YAML-decoded numeric value to an int, rejecting
// non-integral floats.
func toInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case uint64:
		return int(n), nil
	case float64:
		if float64(int(n)) == n {
			return int(n), nil
		}
	}
	return 0, fmt.Errorf("expected integer, got %T (%v)", v, v)
}

// RawDocument parses path as YAML into an untyped Go value without any of
// Load's validation, normalizing mappings to map[string]any. It is what the
// validate tree check feeds to the JSON-Schema validator, so that schema
// validation sees exactly the document shape the loader would.
func RawDocument(path string) (any, error) {
	v, err := rawYAML(path)
	if err != nil {
		return nil, err
	}
	return normalizeYAML(v), nil
}

// rawYAML parses path with yaml.v3 into an untyped Go value, without any of
// Load's schema validation. It is used by Load itself and, separately, by
// the pyyaml cross-check test to compare yaml.v3's parse against pyyaml's.
func rawYAML(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}

// normalizeYAML recursively walks a yaml.v3-decoded value, converting any
// map[interface{}]interface{} node (yaml.v3's fallback for mappings with
// non-string keys) into map[string]any so that jsonval.DeepEqual — which
// only recognizes map[string]any, []any, and Go's scalar/numeric types —
// can compare it against an equivalent JSON-decoded document. Values that
// are already string-keyed maps, slices, or scalars pass through unchanged
// (recursing into their elements).
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = normalizeYAML(vv)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[fmt.Sprintf("%v", k)] = normalizeYAML(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = normalizeYAML(vv)
		}
		return out
	default:
		return v
	}
}
