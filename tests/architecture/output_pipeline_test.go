package architecture_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/openai/openai-cli"

// These checks run against source, including platform-specific production files.
// They need neither Git history nor a generated snapshot. Integration tests may
// import both layers, so *_test.go and testdata are intentionally excluded.
func TestOutputPipelineDependencies(t *testing.T) {
	root := repositoryRoot(t)
	for _, layer := range []string{"custom", "transformers"} {
		t.Run(layer, func(t *testing.T) {
			checkProductionImports(t, root, filepath.Join(root, "pkg", layer), func(filename, imported string) string {
				relative, err := filepath.Rel(root, filename)
				if err != nil {
					t.Fatal(err)
				}
				return prohibitedImport(layer, filepath.ToSlash(relative), imported)
			})
		})
	}
}

// Feature libraries own implementation independently of command registration.
// They may share data-only transformer types, but must never call back into the
// generated command tree or custom runtime that coordinates them.
func TestFeatureLibraryDependencies(t *testing.T) {
	root := repositoryRoot(t)
	for _, library := range []string{
		"clihelp", "readable", "terminalimage", "imagefont", "imagefontmac",
		"imagegallery", "imageoutput", "imagemodels", "imageopen", "imageprefs",
	} {
		t.Run(library, func(t *testing.T) {
			checkProductionImports(t, root, filepath.Join(root, "internal", library), func(_ string, imported string) string {
				for _, prefix := range []string{"pkg/cmd", "pkg/custom", "cmd/openai"} {
					if packageWithin(imported, modulePath+"/"+prefix) {
						return "implementation libraries must not depend on command registration or the executable"
					}
				}
				return ""
			})
		})
	}
}

func checkProductionImports(t *testing.T, root, directory string, prohibited func(string, string) string) {
	t.Helper()
	for _, filename := range productionGoFiles(t, directory) {
		file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			imported, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if reason := prohibited(filename, imported); reason != "" {
				relative, _ := filepath.Rel(root, filename)
				t.Errorf("%s imports %q: %s", relative, imported, reason)
			}
		}
	}
}

// PR227 already owns a terminal renderer in transformers. Keep precisely its
// existing I/O dependencies; new transformations still cannot acquire I/O.
var inheritedRendererImports = map[string]map[string]bool{
	"pkg/transformers/output.go":       {"os": true},
	"pkg/transformers/image_errors.go": {"os": true},
	"pkg/transformers/images.go": {
		"io": true, "net/http": true, "net/url": true, "os": true,
		"github.com/charmbracelet/x/term":                     true,
		"github.com/openai/openai-cli/internal/terminalimage": true,
		"golang.org/x/image/webp":                             true,
	},
}

func prohibitedImport(layer, filename, imported string) string {
	for _, prefix := range []string{modulePath + "/pkg/cmd", modulePath + "/cmd/openai"} {
		if packageWithin(imported, prefix) {
			return "handwritten runtime and transformers must not import generated commands or the executable"
		}
	}
	if layer != "transformers" || inheritedRendererImports[filename][imported] {
		return ""
	}
	if packageWithin(imported, modulePath) {
		if packageWithin(imported, modulePath+"/pkg/transformers") {
			return ""
		}
		return "transformers must remain independent of CLI runtime, internal helpers, and command packages"
	}
	for _, prefix := range []string{
		"os", "io", "bufio", "net", "syscall", "unsafe", "plugin", "runtime",
		"log", "path/filepath", "crypto/rand", "embed", "C",
	} {
		if packageWithin(imported, prefix) {
			return "terminal, file, network, process, and platform operations belong to custom or internal helpers"
		}
	}
	// gjson is the transformer boundary's existing value representation. A new
	// external dependency needs an explicit purity review before being allowed;
	// otherwise terminal or process access could hide behind a helper package.
	first, _, _ := strings.Cut(imported, "/")
	if strings.Contains(first, ".") && imported != "github.com/tidwall/gjson" {
		return "new transformer dependencies need review; keep presentation and I/O libraries outside this layer"
	}
	return ""
}

func packageWithin(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

func productionGoFiles(t *testing.T, directory string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		module, err := os.ReadFile(filepath.Join(directory, "go.mod"))
		if err == nil {
			fields := strings.Fields(string(module))
			if len(fields) >= 2 && fields[0] == "module" && fields[1] == modulePath {
				return directory
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not locate the openai-cli module above the test directory")
		}
		directory = parent
	}
}
