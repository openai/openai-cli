package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// copyNativeShellCLI only replaces fixture compilation when both pins are present.
// The caller still runs a real CLI from its private, quoted-path fixture.
func copyNativeShellCLI(destination string) (bool, error) {
	path, pathSet := os.LookupEnv("OPENAI_CLI_NATIVE_SHELL_BINARY")
	digest, digestSet := os.LookupEnv("OPENAI_CLI_NATIVE_SHELL_SHA256")
	if !pathSet && !digestSet {
		return false, nil
	}
	if !pathSet || !digestSet || path == "" || digest == "" {
		return false, fmt.Errorf("both native shell binary pins must be nonempty")
	}
	if !filepath.IsAbs(path) {
		return false, fmt.Errorf("native shell binary path must be absolute")
	}
	want, err := hex.DecodeString(digest)
	if err != nil || len(want) != sha256.Size {
		return false, fmt.Errorf("native shell binary SHA256 must contain 64 hexadecimal characters")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return false, fmt.Errorf("inspect native shell binary: %w", err)
	}
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return false, fmt.Errorf("native shell binary must be a regular executable file")
	}
	source, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open native shell binary: %w", err)
	}
	defer source.Close()
	opened, err := source.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return false, fmt.Errorf("native shell binary changed while opening")
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, source); err != nil {
		return false, fmt.Errorf("hash native shell binary: %w", err)
	}
	if !bytes.Equal(sum.Sum(nil), want) {
		return false, fmt.Errorf("native shell binary SHA256 mismatch")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("rewind native shell binary: %w", err)
	}
	target, err := os.OpenFile(destination, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return false, fmt.Errorf("create native shell fixture: %w", err)
	}
	complete := false
	defer func() {
		target.Close()
		if !complete {
			os.Remove(destination)
		}
	}()
	if _, err := io.Copy(target, source); err != nil {
		return false, fmt.Errorf("copy native shell binary: %w", err)
	}
	if _, err := target.Seek(0, io.SeekStart); err != nil {
		return false, fmt.Errorf("rewind native shell fixture: %w", err)
	}
	sum.Reset()
	if _, err := io.Copy(sum, target); err != nil {
		return false, fmt.Errorf("hash native shell fixture: %w", err)
	}
	if !bytes.Equal(sum.Sum(nil), want) {
		return false, fmt.Errorf("copied native shell binary SHA256 mismatch")
	}
	if err := target.Close(); err != nil {
		return false, fmt.Errorf("close native shell fixture: %w", err)
	}
	complete = true
	return true, nil
}

func TestNativeShellCLIFixture(t *testing.T) {
	data := []byte("synthetic prebuilt CLI fixture\n")
	source := filepath.Join(t.TempDir(), "source CLI's file")
	if err := os.WriteFile(source, data, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	setPins := func(t *testing.T, path, hash *string) {
		t.Helper()
		for name, value := range map[string]*string{
			"OPENAI_CLI_NATIVE_SHELL_BINARY": path,
			"OPENAI_CLI_NATIVE_SHELL_SHA256": hash,
		} {
			t.Setenv(name, "")
			if value == nil {
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Setenv(name, *value); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Run("absent pins preserve compilation", func(t *testing.T) {
		setPins(t, nil, nil)
		destination := filepath.Join(t.TempDir(), "openai")
		if copied, err := copyNativeShellCLI(destination); copied || err != nil {
			t.Fatalf("unconfigured fixture: copied=%t err=%v", copied, err)
		}
		if _, err := os.Lstat(destination); !os.IsNotExist(err) {
			t.Fatalf("unconfigured fixture created output: %v", err)
		}
	})

	empty, relative, shortHash, invalidHash := "", "openai", "00", strings.Repeat("g", 64)
	wrongHash := strings.Repeat("0", 64)
	missing, directory := filepath.Join(t.TempDir(), "missing"), t.TempDir()
	invalidPath := filepath.Join(source, "child")
	type fixtureCase struct {
		name       string
		path, hash *string
		want       string
	}
	cases := []fixtureCase{
		{"missing path pin", nil, &digest, "both native shell binary pins"},
		{"missing hash pin", &source, nil, "both native shell binary pins"},
		{"empty path pin", &empty, &digest, "both native shell binary pins"},
		{"empty hash pin", &source, &empty, "both native shell binary pins"},
		{"relative path", &relative, &digest, "path must be absolute"},
		{"short hash", &source, &shortHash, "64 hexadecimal characters"},
		{"invalid hash", &source, &invalidHash, "64 hexadecimal characters"},
		{"invalid path", &invalidPath, &digest, "inspect native shell binary"},
		{"missing file", &missing, &digest, "inspect native shell binary"},
		{"directory", &directory, &digest, "regular executable file"},
		{"hash mismatch", &source, &wrongHash, "SHA256 mismatch"},
	}
	if runtime.GOOS != "windows" {
		nonExecutable := filepath.Join(t.TempDir(), "not executable")
		if err := os.WriteFile(nonExecutable, data, 0o600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(source, link); err != nil {
			t.Fatal(err)
		}
		cases = append(cases,
			fixtureCase{"nonexecutable file", &nonExecutable, &digest, "regular executable file"},
			fixtureCase{"symlink", &link, &digest, "regular executable file"})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setPins(t, tc.path, tc.hash)
			destination := filepath.Join(t.TempDir(), "openai")
			copied, err := copyNativeShellCLI(destination)
			if copied || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid fixture: copied=%t err=%v; want %q", copied, err, tc.want)
			}
			if _, err := os.Lstat(destination); !os.IsNotExist(err) {
				t.Fatalf("rejected fixture left output: %v", err)
			}
		})
	}
	t.Run("verified private copy", func(t *testing.T) {
		setPins(t, &source, &digest)
		work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
		if err := os.Mkdir(work, 0o700); err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(work, "openai")
		if copied, err := copyNativeShellCLI(destination); !copied || err != nil {
			t.Fatalf("copy fixture: copied=%t err=%v", copied, err)
		}
		got, err := os.ReadFile(destination)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("copied bytes differ: %q, %v", got, err)
		}
		original, err := os.Stat(source)
		if err != nil {
			t.Fatal(err)
		}
		copied, err := os.Stat(destination)
		if err != nil {
			t.Fatal(err)
		}
		if os.SameFile(original, copied) || !copied.Mode().IsRegular() || runtime.GOOS != "windows" && copied.Mode().Perm() != 0o700 {
			t.Fatalf("fixture must be a separate executable file: %v", copied.Mode())
		}
		got, err = os.ReadFile(source)
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("source bytes changed: %q, %v", got, err)
		}
	})
	t.Run("existing destination preserved", func(t *testing.T) {
		setPins(t, &source, &digest)
		destination := filepath.Join(t.TempDir(), "openai")
		existing := []byte("preserve this fixture")
		if err := os.WriteFile(destination, existing, 0o600); err != nil {
			t.Fatal(err)
		}
		if copied, err := copyNativeShellCLI(destination); copied || err == nil || !strings.Contains(err.Error(), "create native shell fixture") {
			t.Fatalf("existing fixture: copied=%t err=%v", copied, err)
		}
		if got, err := os.ReadFile(destination); err != nil || !bytes.Equal(got, existing) {
			t.Fatalf("existing fixture changed: %q, %v", got, err)
		}
	})
}
