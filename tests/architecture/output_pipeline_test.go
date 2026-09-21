package architecture_test

import (
	"go/ast"
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
			for _, filename := range productionGoFiles(t, filepath.Join(root, "pkg", layer)) {
				file, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.ImportsOnly)
				if err != nil {
					t.Fatal(err)
				}
				for _, spec := range file.Imports {
					path, err := strconv.Unquote(spec.Path.Value)
					if err != nil {
						t.Fatal(err)
					}
					if reason := prohibitedImport(layer, path); reason != "" {
						relative, _ := filepath.Rel(root, filename)
						t.Errorf("%s imports %q: %s", relative, path, reason)
					}
				}
			}
		})
	}
}

func prohibitedImport(layer, imported string) string {
	for _, prefix := range []string{modulePath + "/pkg/cmd", modulePath + "/cmd/openai"} {
		if packageWithin(imported, prefix) {
			return "handwritten runtime and transformers must not import generated commands or the executable"
		}
	}
	if layer != "transformers" {
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

func TestExecutableRemainsThinBootstrap(t *testing.T) {
	root := repositoryRoot(t)
	directory := filepath.Join(root, "cmd", "openai")
	files := productionGoFiles(t, directory)
	if len(files) != 1 || files[0] != filepath.Join(directory, "main.go") {
		t.Fatalf("cmd/openai should contain only main.go as production Go code; move workflows to pkg/custom: %v", files)
	}
	file, err := parser.ParseFile(token.NewFileSet(), files[0], nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if file.Name.Name != "main" {
		t.Fatal("the executable must remain package main")
	}
	wantImports := map[string]bool{"os": false, modulePath + "/pkg/cmd": false, modulePath + "/pkg/custom": false}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if _, allowed := wantImports[path]; !allowed || imported.Name != nil {
			t.Errorf("main.go imports %s; feature and lifecycle dependencies belong in pkg/custom", imported.Path.Value)
		}
		wantImports[path] = true
	}
	for path, found := range wantImports {
		if !found {
			t.Errorf("main.go is missing bootstrap import %q", path)
		}
	}
	var main *ast.FuncDecl
	for _, declaration := range file.Decls {
		if imports, ok := declaration.(*ast.GenDecl); ok && imports.Tok == token.IMPORT {
			continue
		}
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "main" || main != nil {
			t.Fatal("main.go must only declare main; move initialization, state, and helpers to pkg/custom")
		}
		main = function
	}
	if main == nil || main.Body == nil || len(main.Body.List) != 1 || main.Recv != nil || main.Type.Params.NumFields() != 0 || main.Type.Results.NumFields() != 0 {
		t.Fatal("main must only launch custom.Run and pass its exit code to os.Exit")
	}
	statement, ok := main.Body.List[0].(*ast.ExprStmt)
	if !ok {
		t.Fatal("main must launch custom.Run without adding a workflow")
	}
	exit, ok := statement.X.(*ast.CallExpr)
	if !ok || !selector(exit.Fun, "os", "Exit") || len(exit.Args) != 1 {
		t.Fatal("main must pass the CLI exit code to os.Exit")
	}
	run, ok := exit.Args[0].(*ast.CallExpr)
	if !ok || !selector(run.Fun, "custom", "Run") || len(run.Args) != 2 ||
		!selector(run.Args[0], "cmd", "Command") || !selector(run.Args[1], "os", "Args") {
		t.Fatal("main must delegate to custom.Run(cmd.Command, os.Args)")
	}
}

func selector(expression ast.Expr, pkg, name string) bool {
	member, ok := expression.(*ast.SelectorExpr)
	if !ok || member.Sel.Name != name {
		return false
	}
	base, ok := member.X.(*ast.Ident)
	return ok && base.Name == pkg
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
