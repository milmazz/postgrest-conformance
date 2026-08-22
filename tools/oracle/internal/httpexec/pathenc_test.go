package httpexec

import "testing"

func TestEncodeRawPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"/items?id=eq.1", "/items?id=eq.1"},                                               // untouched
		{"/x?a=in.(    )", "/x?a=in.(%20%20%20%20)"},                                       // spaces (case 10204)
		{"/entities?arr=eq.{1,2,3}&select=id", "/entities?arr=eq.%7B1,2,3%7D&select=id"},   // braces (10212)
		{"/simple_pk?k=match.^xy", "/simple_pk?k=match.%5Exy"},                             // caret (1063)
		{"/a?b=like(any).{%plan%,%brain%}", "/a?b=like(any).%7B%25plan%25,%25brain%25%7D"}, // bare % (1086)
		{"/%D9%85%D9%88%D8%A7%D8%B1%D8%AF", "/%D9%85%D9%88%D8%A7%D8%B1%D8%AF"},             // existing escapes kept (1003)
		{"/x?q=a+b", "/x?q=a+b"},                                                           // + untouched
		{`/x?name=eq."q"`, "/x?name=eq.%22q%22"},                                           // double quotes
		{"/تست", "/%D8%AA%D8%B3%D8%AA"},                                                    // raw UTF-8 bytes
		{"/x%2F", "/x%2F"},                                                                 // valid escape at end of string
		{"/x%2", "/x%252"},                                                                 // truncated escape at end -> encode %
	} {
		if got := EncodeRawPath(tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}
