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

// BuildSpec resolves headers/body/JWT per HARNESS §3. injectProfile is the
// Accept-Profile value to add with put-new semantics ("" = none).
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
	if injectProfile != "" && !hasHeader("Accept-Profile") {
		h["Accept-Profile"] = injectProfile
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
