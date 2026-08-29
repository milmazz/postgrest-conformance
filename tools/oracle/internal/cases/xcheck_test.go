package cases

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/jsonval"
)

const pyDump = `
import sys, yaml, json
for f in sys.argv[1:]:
    print(json.dumps({"file": f, "doc": yaml.safe_load(open(f))}, ensure_ascii=False))
`

func TestYAMLCrossCheckAgainstPyYAML(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	if err := exec.Command("python3", "-c", "import yaml").Run(); err != nil {
		t.Skip("pyyaml not available")
	}
	root := repoRoot(t)
	files, _ := filepath.Glob(filepath.Join(root, "cases", "*.yaml"))
	args := append([]string{"-c", pyDump}, files...)
	out, err := exec.Command("python3", args...).Output()
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	checked := 0
	for dec.More() {
		var row struct {
			File string          `json:"file"`
			Doc  json.RawMessage `json:"doc"`
		}
		if err := dec.Decode(&row); err != nil {
			t.Fatal(err)
		}
		pyDoc, err := jsonval.DecodeJSON(row.Doc)
		if err != nil {
			t.Fatal(err)
		}
		goDoc, err := rawYAML(row.File) // exported-for-test: yaml.Unmarshal into any
		if err != nil {
			t.Fatalf("%s: %v", row.File, err)
		}
		if !jsonval.DeepEqual(normalizeYAML(goDoc), pyDoc) {
			t.Errorf("%s: yaml.v3 parse differs from pyyaml", row.File)
		}
		checked++
	}
	if checked != 808 {
		t.Fatalf("cross-checked %d files, want 808", checked)
	}
}
