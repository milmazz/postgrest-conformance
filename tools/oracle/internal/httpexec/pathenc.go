package httpexec

import (
	"fmt"
	"strings"
)

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func EncodeRawPath(p string) string {
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '%':
			if i+2 < len(p) && isHex(p[i+1]) && isHex(p[i+2]) {
				b.WriteByte(c)
			} else {
				b.WriteString("%25")
			}
		case c <= ' ' || c >= 0x7f || strings.IndexByte("\"<>\\^`{}|", c) >= 0:
			fmt.Fprintf(&b, "%%%02X", c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
