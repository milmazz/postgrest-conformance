// Package assert implements the assertion semantics of the repository's
// HARNESS.md §4 ("Assertion semantics") for both HTTP and CLI conformance
// cases. It is the runner's sole authority on what counts as a pass or a
// failure for a given case's `expect:` block.
//
// Every key in a case's Expect map is checked; a key this package doesn't
// recognize for the given case kind is treated as a failure ("fail loudly,
// not silently" per HARNESS §4), not skipped.
package assert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/jsonval"
)

// HTTPResponse is the runner's transport-agnostic capture of an HTTP
// response, taken once so assertion logic never touches the network.
type HTTPResponse struct {
	StatusCode    int
	Reason        string
	Header        http.Header // canonical-key map, all values
	Body          []byte
	ContentLength int64 // -1 if unknown
}

// CLIResult is the runner's capture of a `kind: cli` case's process
// execution, including the optional second dump for dump_reparse_stable.
type CLIResult struct {
	ExitCode  int
	Stdout    []byte
	Stderr    []byte
	Redump    []byte // second dump for dump_reparse_stable
	RedumpRan bool
}

// maxExcerpt bounds how much of a body/value is embedded in a failure
// message, per HARNESS §4's "truncate diff output at ~2KB" guidance.
const maxExcerpt = 2048

// excerpt truncates b for inclusion in a failure message.
func excerpt(b []byte) string {
	if len(b) <= maxExcerpt {
		return string(b)
	}
	return fmt.Sprintf("%s... (truncated, %d bytes total)", b[:maxExcerpt], len(b))
}

// compactJSON re-marshals a decoded (or YAML-decoded expected) value
// compactly for a failure message, truncating per excerpt. Marshaling a
// value built exclusively from jsonval.DecodeJSON/YAML-decoded types
// (string, bool, nil, json.Number, map[string]any, []any) never fails in
// practice; %#v is a defensive fallback only.
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%#v", v)
	}
	return excerpt(b)
}

// foldedHeader looks up name in h (case-insensitive), folding repeated
// values into one string. Set-Cookie's values may legitimately contain
// commas (e.g. an Expires date) and so are joined with "\n" instead of the
// usual ", ". When h has no literal entry for Content-Length but
// contentLength is known (>= 0), the lookup is synthesized from
// contentLength — this covers responses where Go's transport computed the
// length but it may not appear literally in the header map.
func foldedHeader(h http.Header, name string, contentLength int64) (string, bool) {
	vs := h.Values(name)
	if len(vs) == 0 {
		if strings.EqualFold(name, "Content-Length") && contentLength >= 0 {
			return strconv.FormatInt(contentLength, 10), true
		}
		return "", false
	}
	sep := ", "
	if strings.EqualFold(name, "Set-Cookie") {
		sep = "\n"
	}
	return strings.Join(vs, sep), true
}

// expectedScalar renders a YAML-decoded expected value (which may already
// be a string, or a bool/number) as the literal string it must compare
// equal to.
func expectedScalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}

// toStringList normalizes a *_contains expected value, which HARNESS §4
// allows as either a single string or a list of strings, into a slice.
func toStringList(v any) []string {
	switch x := v.(type) {
	case string:
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			out = append(out, expectedScalar(item))
		}
		return out
	default:
		return []string{expectedScalar(x)}
	}
}

// toInt converts a YAML-decoded numeric expect value to an int, rejecting
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
		return 0, fmt.Errorf("non-integral number %v", n)
	default:
		return 0, fmt.Errorf("expected integer, got %T (%v)", v, v)
	}
}

// truthy reports whether v is the boolean true (a missing or non-bool
// predicate value is simply not that predicate).
func truthy(v any) bool {
	b, _ := v.(bool)
	return b
}

// sortedKeys returns m's keys in sorted order, for deterministic failure
// ordering.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CheckHTTP validates r against c's `expect:` block for an HTTP case, per
// HARNESS.md §4. A nil or empty return means every key passed; otherwise
// each element is one self-contained failure description.
func CheckHTTP(c *cases.Case, r *HTTPResponse) []string {
	var failures []string
	for _, k := range sortedKeys(c.Expect) {
		v := c.Expect[k]
		switch k {
		case "status":
			failures = append(failures, checkStatus(r, v)...)
		case "status_text":
			failures = append(failures, checkStatusText(r, v)...)
		case "headers":
			failures = append(failures, checkHeaders(r, v)...)
		case "headers_present":
			failures = append(failures, checkHeadersPresent(r, v)...)
		case "headers_absent":
			failures = append(failures, checkHeadersAbsent(r, v)...)
		case "headers_match":
			failures = append(failures, checkHeadersMatch(r, v)...)
		case "headers_absent_in_value":
			failures = append(failures, checkHeadersAbsentInValue(r, v)...)
		case "headers_no_blank":
			failures = append(failures, checkHeadersNoBlank(r, v)...)
		case "body_exact", "body_json":
			failures = append(failures, checkBodyExact(k, r.Body, v)...)
		case "body_contains":
			failures = append(failures, checkBodyContains(r, v)...)
		case "body_raw":
			failures = append(failures, checkBodyRaw(r, v)...)
		case "body_jsonpath":
			failures = append(failures, checkBodyJSONPath(r, v)...)
		default:
			failures = append(failures, "unhandled expect key "+k)
		}
	}
	return failures
}

// CheckCLI validates r against c's `expect:` block for a `kind: cli` case,
// per HARNESS.md §4. A nil or empty return means every key passed;
// otherwise each element is one self-contained failure description.
func CheckCLI(c *cases.Case, r *CLIResult) []string {
	var failures []string
	for _, k := range sortedKeys(c.Expect) {
		v := c.Expect[k]
		switch k {
		case "exit_code":
			failures = append(failures, checkExitCode(r, v)...)
		case "dump_contains":
			failures = append(failures, checkSubstrings("dump_contains", r.Stdout, v)...)
		case "stderr_contains":
			failures = append(failures, checkSubstrings("stderr_contains", r.Stderr, v)...)
		case "dump_reparse_stable":
			failures = append(failures, checkDumpReparseStable(r, v)...)
		default:
			failures = append(failures, "unhandled expect key "+k)
		}
	}
	return failures
}

func checkStatus(r *HTTPResponse, v any) []string {
	want, err := toInt(v)
	if err != nil {
		return []string{fmt.Sprintf("status: invalid expected value %v: %v", v, err)}
	}
	if r.StatusCode != want {
		return []string{fmt.Sprintf("status: got %d, want %d", r.StatusCode, want)}
	}
	return nil
}

// checkStatusText implements the optional `status_text` assertion (HARNESS
// §4): the exact HTTP reason phrase. It is only meaningful for HTTP clients
// that expose the reason phrase at all.
func checkStatusText(r *HTTPResponse, v any) []string {
	want := expectedScalar(v)
	if r.Reason != want {
		return []string{fmt.Sprintf("status_text: got %q, want %q", r.Reason, want)}
	}
	return nil
}

func checkHeaders(r *HTTPResponse, v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("headers: expected a mapping, got %T", v)}
	}
	var failures []string
	for _, name := range sortedKeys(m) {
		want := expectedScalar(m[name])
		got, found := foldedHeader(r.Header, name, r.ContentLength)
		if !found {
			failures = append(failures, fmt.Sprintf("headers[%s]: missing, want %q", name, want))
			continue
		}
		if got != want {
			failures = append(failures, fmt.Sprintf("headers[%s]: got %q, want %q", name, got, want))
		}
	}
	return failures
}

func checkHeadersPresent(r *HTTPResponse, v any) []string {
	names, ok := v.([]any)
	if !ok {
		return []string{fmt.Sprintf("headers_present: expected a list, got %T", v)}
	}
	var failures []string
	for _, item := range names {
		name := expectedScalar(item)
		if _, found := foldedHeader(r.Header, name, r.ContentLength); !found {
			failures = append(failures, fmt.Sprintf("headers_present[%s]: missing", name))
		}
	}
	return failures
}

func checkHeadersAbsent(r *HTTPResponse, v any) []string {
	names, ok := v.([]any)
	if !ok {
		return []string{fmt.Sprintf("headers_absent: expected a list, got %T", v)}
	}
	var failures []string
	for _, item := range names {
		name := expectedScalar(item)
		if got, found := foldedHeader(r.Header, name, r.ContentLength); found {
			failures = append(failures, fmt.Sprintf("headers_absent[%s]: present with value %q", name, got))
		}
	}
	return failures
}

func checkHeadersMatch(r *HTTPResponse, v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("headers_match: expected a mapping, got %T", v)}
	}
	var failures []string
	for _, name := range sortedKeys(m) {
		pattern := expectedScalar(m[name])
		got, found := foldedHeader(r.Header, name, r.ContentLength)
		if !found {
			failures = append(failures, fmt.Sprintf("headers_match[%s]: missing, want match of %q", name, pattern))
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			failures = append(failures, fmt.Sprintf("headers_match[%s]: invalid pattern %q: %v", name, pattern, err))
			continue
		}
		if !re.MatchString(got) {
			failures = append(failures, fmt.Sprintf("headers_match[%s]: value %q does not match pattern %q", name, got, pattern))
		}
	}
	return failures
}

// checkHeadersAbsentInValue treats a header this case doesn't even mention
// in the response as an empty string, per HARNESS §4, rather than skipping
// it — a missing header trivially contains no forbidden substring.
func checkHeadersAbsentInValue(r *HTTPResponse, v any) []string {
	m, ok := v.(map[string]any)
	if !ok {
		return []string{fmt.Sprintf("headers_absent_in_value: expected a mapping, got %T", v)}
	}
	var failures []string
	for _, name := range sortedKeys(m) {
		got, _ := foldedHeader(r.Header, name, r.ContentLength) // missing => ""
		for _, substr := range toStringList(m[name]) {
			if strings.Contains(got, substr) {
				failures = append(failures, fmt.Sprintf("headers_absent_in_value[%s]: value %q contains forbidden substring %q", name, got, substr))
			}
		}
	}
	return failures
}

// checkHeadersNoBlank asserts that no header anywhere in the response
// (named or not) carries an all-whitespace value.
func checkHeadersNoBlank(r *HTTPResponse, v any) []string {
	want, ok := v.(bool)
	if !ok {
		return []string{fmt.Sprintf("headers_no_blank: expected a bool, got %T", v)}
	}
	if !want {
		return nil
	}
	var failures []string
	for name, vs := range r.Header {
		for _, val := range vs {
			if strings.TrimSpace(val) == "" {
				failures = append(failures, fmt.Sprintf("headers_no_blank: header %q has blank value %q", name, val))
			}
		}
	}
	sort.Strings(failures)
	return failures
}

// checkBodyExact implements the body_exact/body_json synonyms: expected
// nil requires a literally empty body; otherwise the body is JSON-decoded
// and deep-compared to the expected value (key order/whitespace in the raw
// bytes don't matter).
func checkBodyExact(key string, body []byte, v any) []string {
	if v == nil {
		if len(body) != 0 {
			return []string{fmt.Sprintf("%s: got %d-byte body, want empty body", key, len(body))}
		}
		return nil
	}
	got, err := jsonval.DecodeJSON(body)
	if err != nil {
		return []string{fmt.Sprintf("%s: body is not valid JSON: %v (body: %s)", key, err, excerpt(body))}
	}
	if !jsonval.DeepEqual(got, v) {
		return []string{fmt.Sprintf("%s: got %s, want %s", key, compactJSON(got), compactJSON(v))}
	}
	return nil
}

func checkBodyContains(r *HTTPResponse, v any) []string {
	var failures []string
	for _, substr := range toStringList(v) {
		if !bytes.Contains(r.Body, []byte(substr)) {
			failures = append(failures, fmt.Sprintf("body_contains: body does not contain %q", substr))
		}
	}
	return failures
}

func checkBodyRaw(r *HTTPResponse, v any) []string {
	want, ok := v.(string)
	if !ok {
		return []string{fmt.Sprintf("body_raw: expected a string, got %T", v)}
	}
	if string(r.Body) != want {
		return []string{fmt.Sprintf("body_raw: got %q, want %q", excerpt(r.Body), want)}
	}
	return nil
}

// checkBodyJSONPath evaluates each {path, predicate} entry against the
// JSON-decoded body. Each entry carries exactly one predicate: equals,
// present/exists (treated identically), or absent.
func checkBodyJSONPath(r *HTTPResponse, v any) []string {
	entries, ok := v.([]any)
	if !ok {
		return []string{fmt.Sprintf("body_jsonpath: expected a list, got %T", v)}
	}
	doc, err := jsonval.DecodeJSON(r.Body)
	if err != nil {
		return []string{fmt.Sprintf("body_jsonpath: body is not valid JSON: %v (body: %s)", err, excerpt(r.Body))}
	}
	var failures []string
	for _, entryRaw := range entries {
		entry, ok := entryRaw.(map[string]any)
		if !ok {
			failures = append(failures, fmt.Sprintf("body_jsonpath: entry must be a mapping, got %T", entryRaw))
			continue
		}
		path, _ := entry["path"].(string)
		got, found, err := jsonval.EvalPath(doc, path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("body_jsonpath[%s]: %v", path, err))
			continue
		}

		if want, hasEquals := entry["equals"]; hasEquals {
			switch {
			case !found:
				failures = append(failures, fmt.Sprintf("body_jsonpath[%s]: missing, want equal to %s", path, compactJSON(want)))
			case !jsonval.DeepEqual(got, want):
				failures = append(failures, fmt.Sprintf("body_jsonpath[%s]: got %s, want %s", path, compactJSON(got), compactJSON(want)))
			}
			continue
		}
		if truthy(entry["present"]) || truthy(entry["exists"]) {
			if !found {
				failures = append(failures, fmt.Sprintf("body_jsonpath[%s]: missing, want present", path))
			}
			continue
		}
		if truthy(entry["absent"]) {
			if found {
				failures = append(failures, fmt.Sprintf("body_jsonpath[%s]: present with value %s, want absent", path, compactJSON(got)))
			}
			continue
		}
		failures = append(failures, fmt.Sprintf("body_jsonpath[%s]: entry has no recognized predicate", path))
	}
	return failures
}

func checkExitCode(r *CLIResult, v any) []string {
	if s, ok := v.(string); ok {
		if s == "nonzero" {
			if r.ExitCode == 0 {
				return []string{"exit_code: got 0, want nonzero"}
			}
			return nil
		}
		return []string{fmt.Sprintf("exit_code: invalid expected value %q", s)}
	}
	want, err := toInt(v)
	if err != nil {
		return []string{fmt.Sprintf("exit_code: invalid expected value %v: %v", v, err)}
	}
	if r.ExitCode != want {
		return []string{fmt.Sprintf("exit_code: got %d, want %d", r.ExitCode, want)}
	}
	return nil
}

func checkSubstrings(key string, output []byte, v any) []string {
	var failures []string
	for _, substr := range toStringList(v) {
		if !bytes.Contains(output, []byte(substr)) {
			failures = append(failures, fmt.Sprintf("%s: output does not contain %q", key, substr))
		}
	}
	return failures
}

// checkDumpReparseStable requires both that a redump actually ran and that
// it produced output byte-identical to the original dump.
func checkDumpReparseStable(r *CLIResult, v any) []string {
	want, ok := v.(bool)
	if !ok {
		return []string{fmt.Sprintf("dump_reparse_stable: expected a bool, got %T", v)}
	}
	if !want {
		return nil
	}
	if !r.RedumpRan {
		return []string{"dump_reparse_stable: redump did not run"}
	}
	if !bytes.Equal(r.Stdout, r.Redump) {
		return []string{fmt.Sprintf("dump_reparse_stable: got %s, want %s (byte-identical to original dump)", excerpt(r.Redump), excerpt(r.Stdout))}
	}
	return nil
}
