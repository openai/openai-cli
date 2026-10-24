package cli_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// Parse every platform's files without executing commands or requiring a mock server.
func TestRepositoryLayout(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate repository")
	}
	root := filepath.Join(filepath.Dir(file), "../..")
	for _, retired := range []string{"pkg/custom", "pkg/cmd", "pkg/transformers", "scripts/castiron"} {
		if _, err := os.Stat(filepath.Join(root, retired)); !os.IsNotExist(err) {
			t.Errorf("retired path %s must not exist (stat: %v)", retired, err)
		}
	}
	const module = "github.com/openai/openai-cli/"
	for _, dir := range []string{"internal", "generated/commands"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			for _, spec := range parsed.Imports {
				imp, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return err
				}
				if !strings.HasPrefix(imp, module) {
					continue
				}
				imp = strings.TrimPrefix(imp, module)
				isApp := strings.HasPrefix(rel, "internal/app/")
				isFeature := strings.HasPrefix(rel, "internal/commands/")
				isGenerated := strings.HasPrefix(rel, "generated/")
				if withinRepositoryPackage(imp, "pkg") || withinRepositoryPackage(imp, "cmd") ||
					(!isGenerated && withinRepositoryPackage(imp, "generated/commands")) ||
					(!isApp && withinRepositoryPackage(imp, "internal/commands") && !isFeature) ||
					(!isApp && !isGenerated && withinRepositoryPackage(imp, "internal/app")) {
					t.Errorf("%s imports %s across the repository layout boundary", rel, imp)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func withinRepositoryPackage(path, parent string) bool {
	return path == parent || strings.HasPrefix(path, parent+"/")
}

func TestRepositoryLayoutPackageBoundaries(t *testing.T) {
	for _, test := range []struct {
		path, parent string
		within       bool
	}{
		{"internal/commands", "internal/commands", true},
		{"internal/commands/explore", "internal/commands", true},
		{"internal/commands/explore/nested", "internal/commands", true},
		{"internal/commandsuggest", "internal/commands", false},
		{"internal/commandsuggest/nested", "internal/commands", false},
		{"internal/app", "internal/app", true},
		{"internal/app/setup", "internal/app", true},
		{"internal/application", "internal/app", false},
		{"generated/commands", "generated/commands", true},
		{"generated/commands/runtime", "generated/commands", true},
		{"generated/commands_metadata", "generated/commands", false},
	} {
		if got := withinRepositoryPackage(test.path, test.parent); got != test.within {
			t.Errorf("package %q inside %q = %t; want %t", test.path, test.parent, got, test.within)
		}
	}
}
