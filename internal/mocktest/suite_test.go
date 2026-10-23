package mocktest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMain(m *testing.M) {
	os.Exit(RunTests(m))
}

func TestRunTestsHelperExitLeavesNoDirectory(t *testing.T) {
	const child = "OPENAI_MOCKTEST_EXIT_HELPER"
	if os.Getenv(child) == "1" {
		os.Exit(0)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	command := exec.CommandContext(t.Context(), executable, "-test.run=^TestRunTestsHelperExitLeavesNoDirectory$")
	command.Env = append(os.Environ(), child+"=1", "TMPDIR="+directory, "TMP="+directory, "TEMP="+directory)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("helper process: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("helper process leaked %d temporary entries: %v", len(entries), entries)
	}
}

func TestCopyCLIUsesIndependentPrivateFiles(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source")
	want := []byte("synthetic executable")
	if err := os.WriteFile(source, want, 0o700); err != nil {
		t.Fatal(err)
	}
	first, second := filepath.Join(directory, "first"), filepath.Join(directory, "second")
	for _, destination := range []string{first, second} {
		if err := copyCLI(source, destination); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(first, []byte("replaced fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{source, second} {
		contents, err := os.ReadFile(path)
		if err != nil || string(contents) != string(want) {
			t.Fatalf("changing one fixture changed another: contents=%q error=%v", contents, err)
		}
	}
	info, err := os.Stat(second)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("copy permissions = %v, want 0700", info.Mode().Perm())
	}
}

func TestCopyCLIPreservesExistingDestination(t *testing.T) {
	directory := t.TempDir()
	source, destination := filepath.Join(directory, "source"), filepath.Join(directory, "destination")
	for path, contents := range map[string]string{source: "source bytes", destination: "existing fixture"} {
		if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyCLI(source, destination); err == nil {
		t.Fatal("copy overwrote an existing destination")
	}
	contents, err := os.ReadFile(destination)
	if err != nil || string(contents) != "existing fixture" {
		t.Fatalf("existing destination changed: contents=%q error=%v", contents, err)
	}
}
