package assert

import (
	"net/http"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
)

// resp builds an *HTTPResponse from a status/reason/header-map/body,
// mirroring what the runner's real HTTP client would capture.
func resp(status int, reason string, hdr map[string][]string, body string) *HTTPResponse {
	h := http.Header{}
	for k, vs := range hdr {
		for _, v := range vs {
			h.Add(k, v)
		}
	}
	return &HTTPResponse{StatusCode: status, Reason: reason, Header: h,
		Body: []byte(body), ContentLength: int64(len(body))}
}

// mkCase builds a Case with only Expect set, from a YAML fragment.
func mkCase(t *testing.T, expectYAML string) *cases.Case {
	t.Helper()
	var expect map[string]any
	if err := yaml.Unmarshal([]byte(expectYAML), &expect); err != nil {
		t.Fatal(err)
	}
	return &cases.Case{ID: 999, Expect: expect}
}

func containsSubstr(failures []string, substr string) bool {
	for _, f := range failures {
		if strings.Contains(f, substr) {
			return true
		}
	}
	return false
}

// --- status ---

func TestCheckHTTPStatusPass(t *testing.T) {
	c := mkCase(t, `status: 200`)
	r := resp(200, "OK", nil, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPStatusFail(t *testing.T) {
	c := mkCase(t, `status: 200`)
	r := resp(404, "Not Found", nil, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
	if !strings.Contains(got[0], "404") || !strings.Contains(got[0], "200") {
		t.Fatalf("failure must mention both got and want: %q", got[0])
	}
}

// --- status_text ---

func TestCheckHTTPStatusTextPass(t *testing.T) {
	c := mkCase(t, `status_text: "My Custom Status"`)
	r := resp(200, "My Custom Status", nil, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPStatusTextFail(t *testing.T) {
	c := mkCase(t, `status_text: "My Custom Status"`)
	r := resp(200, "OK", nil, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
	if !strings.Contains(got[0], "My Custom Status") || !strings.Contains(got[0], "OK") {
		t.Fatalf("failure must mention both got and want: %q", got[0])
	}
}

// --- headers: named-header-only, case-insensitive name, exact value ---

func TestCheckHTTPHeadersCaseInsensitiveNamePass(t *testing.T) {
	c := mkCase(t, `
headers:
  content-type: application/json; charset=utf-8
`)
	r := resp(200, "OK", map[string][]string{
		"Content-Type": {"application/json; charset=utf-8"},
	}, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersMismatchFail(t *testing.T) {
	c := mkCase(t, `
headers:
  Content-Type: application/json; charset=utf-8
`)
	r := resp(200, "OK", map[string][]string{
		"Content-Type": {"text/plain"},
	}, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
	if !strings.Contains(got[0], "text/plain") || !strings.Contains(got[0], "application/json; charset=utf-8") {
		t.Fatalf("failure must mention both got and want: %q", got[0])
	}
}

func TestCheckHTTPHeadersIntValuedExpectedComparesAsString(t *testing.T) {
	c := mkCase(t, `
headers:
  Content-Length: 2
`)
	r := resp(200, "OK", map[string][]string{
		"Content-Length": {"2"},
	}, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersMissingHeaderFails(t *testing.T) {
	c := mkCase(t, `
headers:
  X-Absent: some-value
`)
	r := resp(200, "OK", nil, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- headers fold: repeated values joined with ", " except Set-Cookie ("\n") ---

func TestCheckHTTPHeadersFoldCommaSpacePass(t *testing.T) {
	c := mkCase(t, `
headers:
  X-Multi: "a, b"
`)
	r := resp(200, "OK", map[string][]string{
		"X-Multi": {"a", "b"},
	}, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersFoldSetCookieNewlinePass(t *testing.T) {
	c := mkCase(t, "headers:\n  Set-Cookie: \"a=1\\nb=2\"\n")
	r := resp(200, "OK", map[string][]string{
		"Set-Cookie": {"a=1", "b=2"},
	}, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersFoldSetCookieCommaJoinFails(t *testing.T) {
	c := mkCase(t, `
headers:
  Set-Cookie: "a=1, b=2"
`)
	r := resp(200, "OK", map[string][]string{
		"Set-Cookie": {"a=1", "b=2"},
	}, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure (Set-Cookie must fold with newline, not comma), got %v", got)
	}
}

// --- Content-Length synthesis from HTTPResponse.ContentLength ---

func TestCheckHTTPContentLengthSynthesizedFromField(t *testing.T) {
	c := mkCase(t, `
headers:
  Content-Length: "5"
`)
	r := &HTTPResponse{
		StatusCode:    200,
		Header:        http.Header{}, // no literal Content-Length entry
		Body:          []byte("hello"),
		ContentLength: 5,
	}
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass (synthesized from ContentLength field), got failures: %v", got)
	}
}

func TestCheckHTTPContentLengthNotSynthesizedWhenUnknown(t *testing.T) {
	c := mkCase(t, `
headers:
  Content-Length: "5"
`)
	r := &HTTPResponse{
		StatusCode:    200,
		Header:        http.Header{},
		Body:          []byte("hello"),
		ContentLength: -1, // unknown: must not synthesize
	}
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure (no synthesis when ContentLength unknown), got %v", got)
	}
}

// --- headers_present ---

func TestCheckHTTPHeadersPresentPass(t *testing.T) {
	c := mkCase(t, `headers_present: [Allow]`)
	r := resp(200, "OK", map[string][]string{"Allow": {"GET, POST"}}, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersPresentFail(t *testing.T) {
	c := mkCase(t, `headers_present: [Allow]`)
	r := resp(200, "OK", nil, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- headers_absent ---

func TestCheckHTTPHeadersAbsentPass(t *testing.T) {
	c := mkCase(t, `headers_absent: [Content-Type]`)
	r := resp(204, "No Content", nil, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersAbsentFail(t *testing.T) {
	c := mkCase(t, `headers_absent: [Content-Type]`)
	r := resp(200, "OK", map[string][]string{"Content-Type": {"application/json"}}, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// TestCheckHTTPHeadersAbsentContentLengthNotSynthesizedPass guards against
// headers_absent[Content-Length] false-positiving on a genuinely
// header-less zero-byte response (e.g. a 204): real PostgREST omits the
// Content-Length header entirely on these, but Go's http.Response still
// reports ContentLength == 0 (not -1/"unknown") for a bodyless response, so
// this must NOT reuse foldedHeader's value-synthesis fallback the way
// checkHeaders/checkHeadersMatch do — headers_present/headers_absent test
// wire presence, not an inferred value. Reproduced against real PostgREST
// v16.0 (case 1311's PATCH .../items?id=eq.2, Accept-Profile:
// representations): the wire response carries no Content-Length header at
// all, so this must pass.
func TestCheckHTTPHeadersAbsentContentLengthNotSynthesizedPass(t *testing.T) {
	c := mkCase(t, `headers_absent: [Content-Length]`)
	r := resp(204, "No Content", nil, "") // no literal Content-Length header
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass (no literal Content-Length header on the wire), got failures: %v", got)
	}
}

// TestCheckHTTPHeadersPresentContentLengthLiteralPass is the mirror
// positive case: a literal Content-Length header must still satisfy
// headers_present.
func TestCheckHTTPHeadersPresentContentLengthLiteralPass(t *testing.T) {
	c := mkCase(t, `headers_present: [Content-Length]`)
	r := resp(200, "OK", map[string][]string{"Content-Length": {"5"}}, "hello")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass (literal Content-Length header present), got failures: %v", got)
	}
}

// --- headers_match ---

func TestCheckHTTPHeadersMatchPass(t *testing.T) {
	c := mkCase(t, `
headers_match:
  Server: "^postgrest/.+"
`)
	r := resp(200, "OK", map[string][]string{"Server": {"postgrest/12.2"}}, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersMatchFail(t *testing.T) {
	c := mkCase(t, `
headers_match:
  Server: "^postgrest/.+"
`)
	r := resp(200, "OK", map[string][]string{"Server": {"bier/1.0"}}, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- headers_absent_in_value ---

func TestCheckHTTPHeadersAbsentInValuePass(t *testing.T) {
	c := mkCase(t, `
headers_absent_in_value:
  Server-Timing: [plan, transaction]
`)
	r := resp(200, "OK", map[string][]string{"Server-Timing": {"jwt;dur=1.2"}}, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersAbsentInValueMissingHeaderTreatedAsEmptyPass(t *testing.T) {
	c := mkCase(t, `
headers_absent_in_value:
  Server-Timing: [plan, transaction]
`)
	r := resp(200, "OK", nil, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass (missing header treated as empty), got failures: %v", got)
	}
}

func TestCheckHTTPHeadersAbsentInValueSubstringPresentFails(t *testing.T) {
	c := mkCase(t, `
headers_absent_in_value:
  Server-Timing: [plan, transaction]
`)
	r := resp(200, "OK", map[string][]string{"Server-Timing": {"plan;dur=1.2"}}, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- headers_no_blank ---

func TestCheckHTTPHeadersNoBlankPass(t *testing.T) {
	c := mkCase(t, `headers_no_blank: true`)
	r := resp(200, "OK", map[string][]string{"X-Ok": {"value"}}, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPHeadersNoBlankFail(t *testing.T) {
	c := mkCase(t, `headers_no_blank: true`)
	r := resp(200, "OK", map[string][]string{"X-Blank": {"   "}}, "")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- body_exact / body_json (synonyms, same semantics) ---

func TestCheckHTTPBodyExactAndBodyJSONMatchPass(t *testing.T) {
	for _, key := range []string{"body_exact", "body_json"} {
		t.Run(key, func(t *testing.T) {
			c := mkCase(t, key+`: [{id: 1}]`)
			r := resp(200, "OK", nil, `[{"id": 1}]`)
			if got := CheckHTTP(c, r); len(got) != 0 {
				t.Fatalf("want pass, got failures: %v", got)
			}
		})
	}
}

func TestCheckHTTPBodyExactAndBodyJSONMismatchFail(t *testing.T) {
	for _, key := range []string{"body_exact", "body_json"} {
		t.Run(key, func(t *testing.T) {
			c := mkCase(t, key+`: [{id: 1}]`)
			r := resp(200, "OK", nil, `[{"id": 2}]`)
			got := CheckHTTP(c, r)
			if len(got) != 1 {
				t.Fatalf("want 1 failure, got %v", got)
			}
		})
	}
}

func TestCheckHTTPBodyExactNullVsEmptyBodyPass(t *testing.T) {
	c := mkCase(t, `body_exact: null`)
	r := resp(204, "No Content", nil, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPBodyExactNullVsLiteralNullBodyFails(t *testing.T) {
	c := mkCase(t, `body_exact: null`)
	r := resp(200, "OK", nil, "null") // 4 bytes, not empty
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure (body must be zero bytes, not literal \"null\"), got %v", got)
	}
}

// TestCheckHTTPBodyExactEmptyStringSentinelPass covers the reference
// implementation's `expected in [nil, ""]` empty-body sentinel (bier's
// Bier.ConformanceAssertions): a case writing `body_exact: ""` (as opposed
// to `body_exact: null`) must also pass against a genuinely empty body.
func TestCheckHTTPBodyExactEmptyStringSentinelPass(t *testing.T) {
	c := mkCase(t, `body_exact: ""`)
	r := resp(204, "No Content", nil, "")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass (\"\" is an empty-body sentinel, same as null), got failures: %v", got)
	}
}

func TestCheckHTTPBodyExactEmptyStringSentinelNonEmptyBodyFails(t *testing.T) {
	c := mkCase(t, `body_exact: ""`)
	r := resp(200, "OK", nil, `{"a":1}`)
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure (body must be zero bytes), got %v", got)
	}
}

func TestCheckHTTPBodyExactKeyOrderAndWhitespaceInsensitivePass(t *testing.T) {
	c := mkCase(t, `
body_exact:
  a: 1
  b: 2
`)
	r := resp(200, "OK", nil, `{"b":2, "a":1}`)
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass (key order/whitespace insensitive), got failures: %v", got)
	}
}

func TestCheckHTTPBodyExactDecodeFailureFails(t *testing.T) {
	c := mkCase(t, `body_exact: [{id: 1}]`)
	r := resp(200, "OK", nil, `not json`)
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure (invalid JSON body), got %v", got)
	}
}

// --- body_contains ---

func TestCheckHTTPBodyContainsSingleStringPass(t *testing.T) {
	c := mkCase(t, `body_contains: "k,extra"`)
	r := resp(200, "OK", nil, "k,extra\nxyyx,u\n")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPBodyContainsSingleStringFail(t *testing.T) {
	c := mkCase(t, `body_contains: "not-there"`)
	r := resp(200, "OK", nil, "k,extra\nxyyx,u\n")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

func TestCheckHTTPBodyContainsListFormPass(t *testing.T) {
	c := mkCase(t, `
body_contains:
  - "a,b"
  - "bar,baz"
`)
	r := resp(200, "OK", nil, "a,b\nbar,baz\n")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPBodyContainsListFormFail(t *testing.T) {
	c := mkCase(t, `
body_contains:
  - "a,b"
  - "missing-entry"
`)
	r := resp(200, "OK", nil, "a,b\nbar,baz\n")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- body_raw ---

func TestCheckHTTPBodyRawPass(t *testing.T) {
	c := mkCase(t, `body_raw: "hello-bytes"`)
	r := resp(200, "OK", nil, "hello-bytes")
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPBodyRawFail(t *testing.T) {
	c := mkCase(t, `body_raw: "hello-bytes"`)
	r := resp(200, "OK", nil, "hello-bytes-extra")
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- body_jsonpath: equals, present, exists, absent (one entry each), plus a failing equals ---

func TestCheckHTTPBodyJSONPathAllPredicatesPass(t *testing.T) {
	c := mkCase(t, `
body_jsonpath:
  - { path: "$.code", equals: "PGRST106" }
  - { path: "$.hint", present: true }
  - { path: "$.hint", exists: true }
  - { path: "$.missing", absent: true }
`)
	r := resp(406, "Not Acceptable", nil, `{"code":"PGRST106","message":"Invalid schema: unknown","hint":"try again"}`)
	if got := CheckHTTP(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckHTTPBodyJSONPathEqualsFail(t *testing.T) {
	c := mkCase(t, `
body_jsonpath:
  - { path: "$.code", equals: "PGRST106" }
`)
	r := resp(406, "Not Acceptable", nil, `{"code":"WRONG"}`)
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

func TestCheckHTTPBodyJSONPathPresentFail(t *testing.T) {
	c := mkCase(t, `
body_jsonpath:
  - { path: "$.hint", present: true }
`)
	r := resp(406, "Not Acceptable", nil, `{"code":"PGRST106"}`)
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

func TestCheckHTTPBodyJSONPathAbsentFail(t *testing.T) {
	c := mkCase(t, `
body_jsonpath:
  - { path: "$.hint", absent: true }
`)
	r := resp(406, "Not Acceptable", nil, `{"hint":"present after all"}`)
	got := CheckHTTP(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- unhandled expect keys ---

func TestCheckHTTPUnhandledCLIOnlyKeyFails(t *testing.T) {
	c := mkCase(t, `exit_code: 0`)
	r := resp(200, "OK", nil, "")
	got := CheckHTTP(c, r)
	if !containsSubstr(got, "unhandled expect key") || !containsSubstr(got, "exit_code") {
		t.Fatalf("want unhandled-key failure mentioning exit_code, got %v", got)
	}
}

func TestCheckCLIUnhandledHTTPOnlyKeyFails(t *testing.T) {
	c := mkCase(t, `status: 200`)
	r := &CLIResult{ExitCode: 0}
	got := CheckCLI(c, r)
	if !containsSubstr(got, "unhandled expect key") || !containsSubstr(got, "status") {
		t.Fatalf("want unhandled-key failure mentioning status, got %v", got)
	}
}

// --- CheckCLI: exit_code ---

func TestCheckCLIExitCodeExactPass(t *testing.T) {
	c := mkCase(t, `exit_code: 0`)
	r := &CLIResult{ExitCode: 0}
	if got := CheckCLI(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckCLIExitCodeExactFail(t *testing.T) {
	c := mkCase(t, `exit_code: 0`)
	r := &CLIResult{ExitCode: 3}
	got := CheckCLI(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

func TestCheckCLIExitCodeNonzeroPass(t *testing.T) {
	c := mkCase(t, `exit_code: nonzero`)
	r := &CLIResult{ExitCode: 3}
	if got := CheckCLI(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckCLIExitCodeNonzeroFail(t *testing.T) {
	c := mkCase(t, `exit_code: nonzero`)
	r := &CLIResult{ExitCode: 0}
	got := CheckCLI(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- CheckCLI: dump_contains / stderr_contains ---

func TestCheckCLIDumpContainsSingleStringPass(t *testing.T) {
	c := mkCase(t, `dump_contains: 'db-schemas = "public"'`)
	r := &CLIResult{Stdout: []byte("db-uri = \"\"\ndb-schemas = \"public\"\n")}
	if got := CheckCLI(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckCLIDumpContainsSingleStringFail(t *testing.T) {
	c := mkCase(t, `dump_contains: 'db-schemas = "public"'`)
	r := &CLIResult{Stdout: []byte("db-uri = \"\"\n")}
	got := CheckCLI(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

func TestCheckCLIDumpContainsListFormPass(t *testing.T) {
	c := mkCase(t, `
dump_contains:
  - 'db-schemas = "public"'
  - 'log-level = "info"'
`)
	r := &CLIResult{Stdout: []byte("db-schemas = \"public\"\nlog-level = \"info\"\n")}
	if got := CheckCLI(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckCLIDumpContainsListFormFail(t *testing.T) {
	c := mkCase(t, `
dump_contains:
  - 'db-schemas = "public"'
  - 'log-level = "info"'
`)
	r := &CLIResult{Stdout: []byte("db-schemas = \"public\"\n")}
	got := CheckCLI(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

func TestCheckCLIStderrContainsPass(t *testing.T) {
	c := mkCase(t, `stderr_contains: "must be at least 32 characters"`)
	r := &CLIResult{Stderr: []byte("The JWT secret must be at least 32 characters long.\n")}
	if got := CheckCLI(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckCLIStderrContainsFail(t *testing.T) {
	c := mkCase(t, `stderr_contains: "must be at least 32 characters"`)
	r := &CLIResult{Stderr: []byte("some unrelated error\n")}
	got := CheckCLI(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

// --- CheckCLI: dump_reparse_stable ---

func TestCheckCLIDumpReparseStablePass(t *testing.T) {
	c := mkCase(t, `dump_reparse_stable: true`)
	r := &CLIResult{
		Stdout:    []byte("db-schemas = \"public\"\n"),
		Redump:    []byte("db-schemas = \"public\"\n"),
		RedumpRan: true,
	}
	if got := CheckCLI(c, r); len(got) != 0 {
		t.Fatalf("want pass, got failures: %v", got)
	}
}

func TestCheckCLIDumpReparseStableDifferingFails(t *testing.T) {
	c := mkCase(t, `dump_reparse_stable: true`)
	r := &CLIResult{
		Stdout:    []byte("db-schemas = \"public\"\n"),
		Redump:    []byte("db-schemas = \"other\"\n"),
		RedumpRan: true,
	}
	got := CheckCLI(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure, got %v", got)
	}
}

func TestCheckCLIDumpReparseStableRedumpDidNotRunFails(t *testing.T) {
	c := mkCase(t, `dump_reparse_stable: true`)
	r := &CLIResult{
		Stdout:    []byte("db-schemas = \"public\"\n"),
		RedumpRan: false,
	}
	got := CheckCLI(c, r)
	if len(got) != 1 {
		t.Fatalf("want 1 failure (redump did not run), got %v", got)
	}
}
