package httpexec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/jsonval"
)

// Known-answer from HARNESS §3.1 (case 1452's literal token): re-sign the
// exact signing input and compare signatures — validates the HMAC/base64url
// mechanics without depending on JSON key order.
func TestHS256KnownAnswer(t *testing.T) {
	signingInput := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJyb2xlIjoicG9zdGdyZXN0X3Rlc3RfYXV0aG9yIiwiaWQiOiJqZG9lIn0"
	wantSig := "B-lReuGNDwAlU1GOC476MlO0vAt9JNoHIlxg2vwMaO0"
	mac := hmac.New(sha256.New, []byte(HS256TestSecret))
	mac.Write([]byte(signingInput))
	if got := base64.RawURLEncoding.EncodeToString(mac.Sum(nil)); got != wantSig {
		t.Fatalf("got %s", got)
	}
}

func TestMintHS256Structure(t *testing.T) {
	tok, err := MintHS256(map[string]any{"role": "r", "exp": "not-a-number"}, HS256TestSecret)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("want 3 segments, got %d", len(parts))
	}
	hdr, _ := base64.RawURLEncoding.DecodeString(parts[0])
	hv, _ := jsonval.DecodeJSON(hdr)
	if !jsonval.DeepEqual(hv, map[string]any{"alg": "HS256", "typ": "JWT"}) {
		t.Fatalf("header: %s", hdr)
	}
	pl, _ := base64.RawURLEncoding.DecodeString(parts[1])
	pv, _ := jsonval.DecodeJSON(pl)
	// payload signed as-is: the invalid exp string must survive untouched
	if !jsonval.DeepEqual(pv, map[string]any{"role": "r", "exp": "not-a-number"}) {
		t.Fatalf("payload: %s", pl)
	}
	mac := hmac.New(sha256.New, []byte(HS256TestSecret))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if base64.RawURLEncoding.EncodeToString(mac.Sum(nil)) != parts[2] {
		t.Fatal("signature does not verify")
	}
}
