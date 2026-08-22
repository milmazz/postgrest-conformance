package httpexec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
)

const HS256TestSecret = "reallyreallyreallyreallyverysafe"

func MintHS256(payload map[string]any, secret string) (string, error) {
	pl, err := marshalNoHTMLEscape(payload) // claim values may contain URLs; keep bytes literal
	if err != nil {
		return "", err
	}
	b64 := base64.RawURLEncoding
	signing := b64.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." + b64.EncodeToString(pl)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signing))
	return signing + "." + b64.EncodeToString(mac.Sum(nil)), nil
}
