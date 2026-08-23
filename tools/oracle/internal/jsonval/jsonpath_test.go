package jsonval

import "testing"

func doc(t *testing.T) any {
	v, err := DecodeJSON([]byte(`{
  "info": {"title": "T"},
  "paths": {"/child_entities": {"get": {"parameters": [{"$ref": "#/x"}], "responses": {"200": {"description": "OK"}}}}},
  "security": [{"JWT": []}],
  "arr": [{"Plan": "p"}]
}`))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestEvalPath(t *testing.T) {
	d := doc(t)
	for _, tc := range []struct {
		path  string
		found bool
		want  any
	}{
		{"$.info.title", true, "T"},
		{"$.paths['/child_entities'].get.parameters[0]['$ref']", true, "#/x"},
		{"$.paths['/child_entities'].get.responses['200'].description", true, "OK"},
		{"$.security[0].JWT", true, []any{}},
		{"$.missing", false, nil},
		{"$.info.title.deeper", false, nil},
		{"$.security[5]", false, nil},
	} {
		v, found, err := EvalPath(d, tc.path)
		if err != nil {
			t.Fatalf("%s: %v", tc.path, err)
		}
		if found != tc.found {
			t.Fatalf("%s: found=%v want %v", tc.path, found, tc.found)
		}
		if found && !DeepEqual(v, tc.want) {
			t.Fatalf("%s: got %v", tc.path, v)
		}
	}
}

func TestEvalPathRootIndex(t *testing.T) {
	v, _ := DecodeJSON([]byte(`[{"Plan": "p"}]`))
	got, found, err := EvalPath(v, "$[0].Plan")
	if err != nil || !found || got != "p" {
		t.Fatalf("got %v %v %v", got, found, err)
	}
}

func TestEvalPathRejectsUnsupported(t *testing.T) {
	for _, p := range []string{"info.title", "$..x", "$.a[*]", "$.a[?(@.b)]", "$['a\\'b']"} {
		if _, _, err := EvalPath(nil, p); err == nil {
			t.Fatalf("%s: want syntax error", p)
		}
	}
}
