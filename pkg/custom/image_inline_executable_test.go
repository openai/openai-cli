package custom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeImageExecutableFixture(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImageInlineExecutableSurvivesGoRunCleanup(t *testing.T) {
	root := t.TempDir()
	checkout := filepath.Join(root, "cli ' workspace $(literal)")
	writeImageExecutableFixture(t, filepath.Join(checkout, "go.mod"), "module github.com/openai/openai-cli\n")
	writeImageExecutableFixture(t, filepath.Join(checkout, "cmd", "openai", "main.go"), "package main\n")
	temporary := filepath.Join(root, "go temp")
	executable := writeImageExecutableFixture(t, filepath.Join(temporary, "go-build123456", "b001", "exe", "openai"), "synthetic binary")
	command := imageInlineExecutableCommand(executable, "", filepath.Join(checkout, "cmd", "openai"), temporary)
	want := "go -C '" + filepath.Join(root, "cli ") + "'\\'' workspace $(literal)' run ./cmd/openai"
	if command != want || strings.Contains(command, "go-build123456") {
		t.Fatalf("temporary executable did not become a safe persistent command: got=%q want=%q", command, want)
	}
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(checkout, "cmd", "openai")); err != nil {
		t.Fatal("suggested source command did not survive executable cleanup")
	}
}

func TestImageInlineExecutableKeepsNormalBinarySelection(t *testing.T) {
	root := t.TempDir()
	executable := writeImageExecutableFixture(t, filepath.Join(root, "normal build", "openai"), "synthetic binary")
	other := writeImageExecutableFixture(t, filepath.Join(root, "older install", "openai"), "other binary")
	for _, installed := range []string{"", other} {
		if got := imageInlineExecutableCommand(executable, installed, root, root); got != quoteImageShellArgument(executable) {
			t.Fatalf("exact binary was replaced: %q", got)
		}
	}
	if got := imageInlineExecutableCommand(executable, executable, root, root); got != "openai" {
		t.Fatalf("same installed executable not reused: %q", got)
	}
}

func TestImageInlineGoRunRequiresExactTemporaryLayout(t *testing.T) {
	root := t.TempDir()
	for _, tt := range []struct {
		name string
		want bool
	}{
		{"go-build123/b001/exe/openai", true},
		{"go-build123/b001/exe/openai.exe", true},
		{"go-build/b001/exe/openai", false},
		{"go-buildabc/b001/exe/openai", false},
		{"go-build123/bx01/exe/openai", false},
		{"go-build123/b001/openai", false},
		{"go-build123/b001/exe/other", false},
		{"nested/go-build123/b001/exe/openai", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := writeImageExecutableFixture(t, filepath.Join(root, filepath.FromSlash(tt.name)), "synthetic")
			if got := isTemporaryImageExecutable(path, root); got != tt.want {
				t.Fatalf("temporary layout match=%v want=%v", got, tt.want)
			}
		})
	}
}

func TestImageInlineGoRunResolvesTemporaryDirectoryAlias(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	path := writeImageExecutableFixture(t, filepath.Join(real, "go-build123", "b001", "exe", "openai"), "synthetic")
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if !isTemporaryImageExecutable(path, alias) {
		t.Fatal("temporary-directory symlink prevented go run detection")
	}
}

func TestImageInlineGoRunRequiresKnownCheckout(t *testing.T) {
	for _, tt := range []struct {
		name, module string
		entrypoint   bool
		want         bool
	}{
		{"exact module", "module github.com/openai/openai-cli\n", true, true},
		{"quoted module", "module \"github.com/openai/openai-cli\" // CLI module\n", true, true},
		{"leading comment", "// CLI module\n\nmodule github.com/openai/openai-cli\n", true, true},
		{"commented module", "/*\nmodule github.com/openai/openai-cli\n*/\nmodule example.com/other\n", true, false},
		{"other module", "module example.com/other\n", true, false},
		{"prefix only", "module github.com/openai/openai-cli-extra\n", true, false},
		{"missing module", "go 1.25\n", true, false},
		{"missing entrypoint", "module github.com/openai/openai-cli\n", false, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeImageExecutableFixture(t, filepath.Join(root, "go.mod"), tt.module)
			if tt.entrypoint {
				writeImageExecutableFixture(t, filepath.Join(root, "cmd", "openai", "main.go"), "package main\n")
			}
			got := imageInlineCheckout(root)
			if (got != "") != tt.want || tt.want && got != root {
				t.Fatalf("checkout=%q want match=%v", got, tt.want)
			}
		})
	}
}

func TestImageInlineGoRunDoesNotEscapeNestedModule(t *testing.T) {
	root := t.TempDir()
	writeImageExecutableFixture(t, filepath.Join(root, "go.mod"), "module github.com/openai/openai-cli\n")
	writeImageExecutableFixture(t, filepath.Join(root, "cmd", "openai", "main.go"), "package main\n")
	nested := filepath.Join(root, "api_reference")
	writeImageExecutableFixture(t, filepath.Join(nested, "go.mod"), "module example.com/reference\n")
	if got := imageInlineCheckout(nested); got != "" {
		t.Fatalf("nested module was mistaken for outer CLI checkout: %q", got)
	}
	if got := imageInlineCheckout(root + "\ninvalid"); got != "" {
		t.Fatalf("control characters accepted in source command: %q", got)
	}
}
