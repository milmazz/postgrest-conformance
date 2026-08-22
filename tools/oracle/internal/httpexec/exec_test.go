package httpexec

import (
	"bufio"
	"net"
	"strings"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/cases"
)

func TestBuildSpecProfileInjection(t *testing.T) {
	c := &cases.Case{Request: cases.Request{Method: "GET", Path: "/x",
		Headers: map[string]string{"Accept": "application/json"}}}
	s, _ := BuildSpec(c, "operators")
	if s.Headers["Accept-Profile"] != "operators" {
		t.Fatal("must inject Accept-Profile")
	}
	// put-new: explicit Accept-Profile wins
	c.Request.Headers = map[string]string{"Accept-Profile": "v2"}
	s, _ = BuildSpec(c, "headers")
	if s.Headers["Accept-Profile"] != "v2" {
		t.Fatal("explicit Accept-Profile must win")
	}
	// Content-Profile does NOT suppress injection (INDEX.md caveat)
	c.Request.Headers = map[string]string{"Content-Profile": "v2"}
	s, _ = BuildSpec(c, "headers")
	if s.Headers["Accept-Profile"] != "headers" {
		t.Fatal("Content-Profile must not suppress injection")
	}
}

func TestBuildSpecJWT(t *testing.T) {
	c := &cases.Case{Request: cases.Request{Method: "GET", Path: "/x",
		JWT: &cases.JWT{SignWith: "hs256_test_secret", Payload: map[string]any{"role": "r"}}}}
	s, err := BuildSpec(c, "")
	if err != nil || !strings.HasPrefix(s.Headers["Authorization"], "Bearer ey") {
		t.Fatalf("want minted bearer, got %q (%v)", s.Headers["Authorization"], err)
	}
	// explicit Authorization wins over jwt block
	c.Request.Headers = map[string]string{"Authorization": "Bearer literal"}
	s, _ = BuildSpec(c, "")
	if s.Headers["Authorization"] != "Bearer literal" {
		t.Fatal("explicit Authorization must win")
	}
	// unknown sign_with is an error
	c.Request.Headers = nil
	c.Request.JWT.SignWith = "nope"
	if _, err := BuildSpec(c, ""); err == nil {
		t.Fatal("want unknown sign_with error")
	}
}

func TestBuildSpecBodies(t *testing.T) {
	mk := func(r cases.Request) *Spec {
		s, err := BuildSpec(&cases.Case{Request: r}, "")
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if s := mk(cases.Request{BodyRaw: "a,b\n1,2", HasBodyRaw: true}); string(s.Body) != "a,b\n1,2" {
		t.Fatal("body_raw verbatim")
	}
	if s := mk(cases.Request{BodyJSON: "str", HasBodyJSON: true}); string(s.Body) != `"str"` {
		t.Fatal("body_json always JSON-encodes")
	}
	if s := mk(cases.Request{Body: "str", HasBody: true}); string(s.Body) != "str" {
		t.Fatal("body: string sent as-is")
	}
	if s := mk(cases.Request{Body: map[string]any{"a": 1}, HasBody: true}); string(s.Body) != `{"a":1}` {
		t.Fatal("body: non-string JSON-encoded")
	}
	if s := mk(cases.Request{}); s.HasBody {
		t.Fatal("no body key = no body")
	}
}

func TestDoRawWire(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	gotLine := make(chan string, 1)
	go func() {
		conn, _ := ln.Accept()
		defer conn.Close()
		br := bufio.NewReader(conn)
		line, _ := br.ReadString('\n')
		gotLine <- strings.TrimRight(line, "\r\n")
		for { // drain headers
			h, _ := br.ReadString('\n')
			if h == "\r\n" || h == "\n" || h == "" {
				break
			}
		}
		conn.Write([]byte("HTTP/1.1 419 My Custom Status\r\n" +
			"Set-Cookie: a=1\r\n" +
			"Set-Cookie: b=2; Expires=Wed, 21 Oct 2015 07:28:00 GMT\r\n" +
			"Content-Length: 2\r\n\r\nhi"))
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r, err := Do(port, &Spec{Method: "GET", Path: "/x?a=in.( )", Headers: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	if line := <-gotLine; line != "GET /x?a=in.(%20) HTTP/1.1" {
		t.Fatalf("request line: %q", line)
	}
	if r.StatusCode != 419 || r.Reason != "My Custom Status" {
		t.Fatalf("status: %d %q", r.StatusCode, r.Reason)
	}
	if got := r.Header.Values("Set-Cookie"); len(got) != 2 {
		t.Fatalf("set-cookie values: %v", got)
	}
	if string(r.Body) != "hi" || r.ContentLength != 2 {
		t.Fatalf("body/CL: %q %d", r.Body, r.ContentLength)
	}
}
