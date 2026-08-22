package jsonval

import "testing"

func mustNum(t *testing.T, s string) any {
	v, err := DecodeJSON([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestDecodeUsesNumber(t *testing.T) {
	v, err := DecodeJSON([]byte(`{"big": 9999999999999999999, "f": 1.5}`))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if _, ok := m["big"].(interface{ String() string }); !ok {
		t.Fatalf("big decoded as %T, want json.Number", m["big"])
	}
}

func TestDeepEqualNumbers(t *testing.T) {
	a, _ := DecodeJSON([]byte(`[{"id": 2}, 2.0, 9999999999999999999, "2"]`))
	// YAML-decoded shape: ints and float64s
	b := []any{map[string]any{"id": 2}, float64(2), mustNum(t, "9999999999999999999"), "2"}
	if !DeepEqual(a, b) {
		t.Fatal("want equal")
	}
	if DeepEqual(float64(2), "2") {
		t.Fatal("number must not equal string")
	}
	if DeepEqual(map[string]any{"a": 1}, map[string]any{"a": 1, "b": 2}) {
		t.Fatal("extra key must not be equal")
	}
	if DeepEqual([]any{1, 2}, []any{2, 1}) {
		t.Fatal("order matters")
	}
	if !DeepEqual(nil, nil) || DeepEqual(nil, "x") {
		t.Fatal("nil handling")
	}
}

func TestDecodeRejectsTrailingData(t *testing.T) {
	for _, tc := range []struct {
		input string
		valid bool
	}{
		{"{}{}", false},
		{"{} garbage", false},
		{"{}]", false},
		{"[1,2] extra", false},
		{`{"a":1}`, true},
		{"[1,2]\n  ", true},
	} {
		_, err := DecodeJSON([]byte(tc.input))
		if tc.valid && err != nil {
			t.Fatalf("%q: want valid, got %v", tc.input, err)
		}
		if !tc.valid && err == nil {
			t.Fatalf("%q: want error, got nil", tc.input)
		}
	}
}
