package core

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCoreHasNoDesktopToolkitImports guards the Core boundary: the pure-Go
// core must not import any package path of the desktop toolkit, directly or
// through a document parser. The parser lives in internal/markdown instead.
func TestCoreHasNoDesktopToolkitImports(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, imp := range f.Imports {
			path, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("%s: bad import %s", name, imp.Path.Value)
			}
			if path == "github.com/egoist/mygo" || strings.HasPrefix(path, "github.com/egoist/mygo/") {
				t.Errorf("%s imports desktop toolkit package %q", name, path)
			}
		}
	}
	data, err := os.ReadFile("documentation.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"gorequest/internal/markdown"`) {
		t.Error("documentation.go should parse through internal/markdown")
	}
}
