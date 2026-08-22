package httpexec

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/assert"
	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
)

// Spec is a fully resolved HTTP request ready to be sent over the wire:
// headers include any injected Accept-Profile / minted Authorization, and
// Body/HasBody reflect the case's body precedence (HARNESS §3.5).
type Spec struct {
	Method, Path string
	Headers      map[string]string
	Body         []byte
	HasBody      bool
}

// marshalNoHTMLEscape JSON-encodes v with HTML-escaping disabled (claim/body
// values may contain characters like < > & that must survive literally) and
// with the trailing newline json.Encoder appends trimmed off.
func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

var sharedTransport = &http.Transport{
	DisableCompression:  true, // preserve wire Content-Length; never send Accept-Encoding
	Proxy:               nil,
	MaxIdleConnsPerHost: 4,
}

var client = &http.Client{
	Transport: sharedTransport,
	Timeout:   30 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse // never follow; cases assert the 3xx itself
	},
}

// writeMethods are the HTTP methods for which PostgREST selects the
// request's schema via Content-Profile rather than Accept-Profile.
//
// Per PostgREST's own docs (docs/references/api/schemas.rst, "Multiple
// schemas"): GET/HEAD select the schema with Accept-Profile; "other
// methods" — POST, PATCH, PUT, DELETE — select it with Content-Profile.
// This applies uniformly to tables *and* functions (RPC), including a POST
// to /rpc/<fn>, which PostgREST treats as a write for profile-selection
// purposes regardless of the function's own volatility.
//
// Confirmed empirically against the pinned v16.0 binary: sending
// Accept-Profile on a write is not an error, it is silently ignored and
// the request resolves against the *default* schema instead (reproduced
// with case 1755, POST /rpc/ret_point_overloaded, schema: observability —
// curl -v against a manually booted instance showed the write landing in
// the default "test" schema's namesake object, not observability's, for
// Accept-Profile, and correctly in observability's for Content-Profile).
// Case 1011's own note already says as much directly: "Write methods use
// Content-Profile (not Accept-Profile) for schema selection".
var writeMethods = map[string]bool{
	"POST": true, "PATCH": true, "PUT": true, "DELETE": true,
}

// profileHeaderName returns the header BuildSpec should inject a case's
// schema label into, based on its request method.
func profileHeaderName(method string) string {
	if writeMethods[strings.ToUpper(method)] {
		return "Content-Profile"
	}
	return "Accept-Profile"
}

// BuildSpec resolves headers/body/JWT per HARNESS §3. injectProfile is the
// schema label to add — as Content-Profile for a write method (POST/PATCH/
// PUT/DELETE) or Accept-Profile otherwise (GET/HEAD/OPTIONS) — with
// put-new semantics ("" = no injection; an explicit header of that same
// name in the case's own request.headers wins).
func BuildSpec(c *cases.Case, injectProfile string) (*Spec, error) {
	h := map[string]string{}
	for k, v := range c.Request.Headers {
		h[k] = v
	}
	hasHeader := func(name string) bool {
		for k := range h {
			if strings.EqualFold(k, name) {
				return true
			}
		}
		return false
	}
	if injectProfile != "" {
		name := profileHeaderName(c.Request.Method)
		if !hasHeader(name) {
			h[name] = injectProfile
		}
	}
	if c.Request.JWT != nil && !hasHeader("Authorization") {
		if c.Request.JWT.SignWith != "hs256_test_secret" {
			return nil, fmt.Errorf("case %d: unknown sign_with %q", c.ID, c.Request.JWT.SignWith)
		}
		tok, err := MintHS256(c.Request.JWT.Payload, HS256TestSecret)
		if err != nil {
			return nil, err
		}
		h["Authorization"] = "Bearer " + tok
	}
	s := &Spec{Method: c.Request.Method, Path: c.Request.Path, Headers: h}
	switch {
	case c.Request.HasBodyRaw:
		s.Body, s.HasBody = []byte(c.Request.BodyRaw), true
	case c.Request.HasBodyJSON:
		b, err := marshalNoHTMLEscape(c.Request.BodyJSON)
		if err != nil {
			return nil, err
		}
		s.Body, s.HasBody = b, true
	case c.Request.HasBody:
		if str, ok := c.Request.Body.(string); ok {
			s.Body, s.HasBody = []byte(str), true
		} else {
			b, err := marshalNoHTMLEscape(c.Request.Body)
			if err != nil {
				return nil, err
			}
			s.Body, s.HasBody = b, true
		}
	}
	return s, nil
}

// Do sends the spec to 127.0.0.1:port over plain HTTP/1.1.
func Do(port int, s *Spec) (*assert.HTTPResponse, error) {
	u := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port), Opaque: EncodeRawPath(s.Path)}
	var body io.Reader
	if s.HasBody {
		body = bytes.NewReader(s.Body)
	}
	req, err := http.NewRequest(s.Method, "http://placeholder/", body)
	if err != nil {
		return nil, err
	}
	req.URL = u
	req.Host = u.Host
	for k, v := range s.Headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode))
	reason = strings.TrimPrefix(reason, " ")
	return &assert.HTTPResponse{
		StatusCode: resp.StatusCode, Reason: reason,
		Header: resp.Header, Body: b, ContentLength: resp.ContentLength,
	}, nil
}
