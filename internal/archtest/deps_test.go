// Package archtest enforces the package dependency rules of docs/architecture.md A4.
package archtest

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

const module = "github.com/alec-jensen/sofar/internal/"

// pure packages may import only these internal packages.
var allowed = map[string][]string{
	"money":  {},
	"civil":  {},
	"budget": {"money", "civil"},
}

func TestA4_PurePackagesImportNothingElseInternal(t *testing.T) {
	for pkg, ok := range allowed {
		out, err := exec.Command("go", "list", "-json", "../"+pkg).Output()
		if err != nil {
			t.Fatalf("go list %s: %v", pkg, err)
		}
		var info struct{ Imports, TestImports []string }
		if err = json.Unmarshal(out, &info); err != nil {
			t.Fatal(err)
		}
		for _, imp := range append(info.Imports, info.TestImports...) {
			if !strings.HasPrefix(imp, module) {
				continue
			}
			name := strings.TrimPrefix(imp, module)
			fine := false
			for _, a := range ok {
				fine = fine || name == a
			}
			if !fine {
				t.Errorf("internal/%s must not import internal/%s (architecture A4)", pkg, name)
			}
		}
	}
}

func TestA4_PurePackagesDoNotDoIO(t *testing.T) {
	banned := []string{"database/sql", "net/http", "os", "io/ioutil", "log"}
	for pkg := range allowed {
		out, err := exec.Command("go", "list", "-json", "../"+pkg).Output()
		if err != nil {
			t.Fatal(err)
		}
		var info struct{ Imports []string }
		if err = json.Unmarshal(out, &info); err != nil {
			t.Fatal(err)
		}
		for _, imp := range info.Imports {
			for _, b := range banned {
				if imp == b {
					t.Errorf("internal/%s imports %s: the engine must be free of I/O", pkg, b)
				}
			}
		}
	}
}
