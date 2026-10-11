package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainDispatchOutputManpagesDeepSubgroups(t *testing.T) {
	dir := t.TempDir()
	got := runMainDispatch(t, "bash", "openai", "@manpages", "--text", "-o", dir)
	if got.code != 0 || got.stdout != "" || got.stderr != "Wrote manpages to "+dir+"\n" {
		t.Fatalf("manpage generation failed: %+v", got)
	}
	text, err := os.ReadFile(filepath.Join(dir, "man1", "openai.1"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(dir, "man1", "openai.1.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	uncompressed, err := io.ReadAll(compressed)
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != string(uncompressed) {
		t.Fatal("text and gzip manpages differ")
	}
	for _, line := range strings.Split(string(text), "\n") {
		if strings.HasPrefix(line, "#######") {
			t.Fatalf("Markdown heading leaked into roff: %q", line)
		}
	}
	for _, path := range []string{
		"admin projects users roles create",
		"admin projects service-accounts api-keys create",
		"projects groups roles delete",
		"audio transcribe",
		"transcribe",
		"files upload",
		"tokenizer",
		"tokenizer count",
		"tokenizer inspect",
		"tokenizer encodings",
		"tokenizer licenses",
		"codex",
	} {
		if !strings.Contains(string(text), "\n.SH "+path+"\n") {
			t.Errorf("missing full-path heading for %s", path)
		}
	}
	for _, hidden := range []string{"@manpages", "__complete", "__preview", "__output", "admin:organization", "\n.SH admin organization", "\n.SH audio transcriptions"} {
		if strings.Contains(string(text), hidden) {
			t.Errorf("hidden command %s leaked into manpage", hidden)
		}
	}
}

func TestMainDispatchOutputManpageReceiptPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		root, local                  []string
		text, gzip, receipt, verbose bool
		defaultDirectory             bool
	}{
		{name: "default gzip and directory", gzip: true, receipt: true, defaultDirectory: true},
		{name: "text only", local: []string{"--text", "--gzip=false"}, text: true, receipt: true},
		{name: "both formats", local: []string{"--text"}, text: true, gzip: true, receipt: true},
		{name: "neither format", local: []string{"--text=false", "--gzip=false"}},
		{name: "quiet verbose", root: []string{"--quiet", "--verbose"}, local: []string{"--text"}, text: true, gzip: true},
		{name: "nested quiet", local: []string{"--text", "--quiet", "--verbose"}, text: true, gzip: true},
		{name: "inherited machine errors", root: []string{"--format", "json"}, local: []string{"--text"}, text: true, gzip: true},
		{name: "explicit machine errors", root: []string{"--format-error", "json"}, local: []string{"--text"}, text: true, gzip: true},
		{name: "extracted errors", root: []string{"--transform-error", "message"}, local: []string{"--text"}, text: true, gzip: true},
		{name: "verbose text override", root: []string{"--format", "json", "--format-error", "text", "--verbose"}, local: []string{"--text"}, text: true, gzip: true, receipt: true, verbose: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			t.Chdir(work)
			dir := filepath.Join(work, "manual with spaces")
			args := append(append([]string{"openai"}, tc.root...), "@manpages")
			args = append(args, tc.local...)
			if tc.defaultDirectory {
				dir = "man"
			} else {
				args = append(args, "--output", dir)
			}
			got := runMainDispatch(t, "bash", args...)
			stderr := got.stderr
			if tc.verbose {
				stderr, _ = removeVerboseElapsed(t, stderr)
			}
			want := ""
			if tc.receipt {
				want = "Wrote manpages to " + dir + "\n"
			}
			if tc.verbose {
				want += "Command: @manpages\nFormat option: json\nCommand result: completed\n"
			}
			if got.code != 0 || got.stdout != "" || stderr != want {
				t.Fatalf("manpage receipt policy: got=%+v want stderr=%q", got, want)
			}
			info, err := os.Stat(filepath.Join(dir, "man1"))
			if err != nil || !info.IsDir() {
				t.Fatalf("man1 directory: %v %v", info, err)
			}
			var text []byte
			for _, output := range []struct {
				name string
				want bool
			}{{"openai.1", tc.text}, {"openai.1.gz", tc.gzip}} {
				data, err := os.ReadFile(filepath.Join(dir, "man1", output.name))
				if !output.want {
					if !os.IsNotExist(err) {
						t.Fatalf("unselected output %s exists: %v", output.name, err)
					}
					continue
				}
				if err != nil || len(data) == 0 {
					t.Fatalf("selected output %s: bytes=%d err=%v", output.name, len(data), err)
				}
				if output.name == "openai.1" {
					text = data
					continue
				}
				reader, err := gzip.NewReader(bytes.NewReader(data))
				if err != nil {
					t.Fatal(err)
				}
				decoded, readErr := io.ReadAll(reader)
				closeErr := reader.Close()
				if readErr != nil || closeErr != nil || !bytes.Contains(decoded, []byte(".SH NAME")) {
					t.Fatalf("incomplete gzip manpage: read=%v close=%v", readErr, closeErr)
				}
				if tc.text && !bytes.Equal(text, decoded) {
					t.Fatal("selected text and gzip content differ")
				}
			}
		})
	}
}

func TestMainDispatchOutputManpageFailures(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		for _, machine := range []bool{false, true} {
			t.Run(fmt.Sprintf("directory failure/formats disabled=%t/machine=%t", disabled, machine), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "existing file")
				if err := os.WriteFile(path, []byte("GOOD"), 0600); err != nil {
					t.Fatal(err)
				}
				args := []string{"openai", "--quiet", "--verbose"}
				if machine {
					args = append(args, "--format-error", "json")
				}
				args = append(args, "@manpages", "--output", path)
				if disabled {
					args = append(args, "--text=false", "--gzip=false")
				}
				got := runMainDispatch(t, "bash", args...)
				assertManpageFailure(t, got, machine)
				if data, err := os.ReadFile(path); err != nil || string(data) != "GOOD" {
					t.Fatalf("directory failure changed existing file: %q %v", data, err)
				}
			})
		}
	}
	t.Run("completed text survives gzip creation failure", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, "man1", "openai.1.gz"), 0755); err != nil {
			t.Fatal(err)
		}
		got := runMainDispatch(t, "bash", "openai", "@manpages", "--text", "-o", dir)
		assertManpageFailure(t, got, false)
		text, err := os.ReadFile(filepath.Join(dir, "man1", "openai.1"))
		if err != nil || !bytes.Contains(text, []byte(".SH admin projects users roles create")) {
			t.Fatalf("completed text was lost: bytes=%d err=%v", len(text), err)
		}
	})
}

func assertManpageFailure(t *testing.T, got mainDispatchResult, machine bool) {
	t.Helper()
	if got.code != 1 || got.stdout != "" || got.stderr == "" || strings.Contains(got.stderr, "Wrote manpages") || strings.Contains(got.stderr, "Command result:") {
		t.Fatalf("manpage failure lost status/data or reported completion: %+v", got)
	}
	if machine {
		var payload map[string]any
		err := json.Unmarshal([]byte(got.stderr), &payload)
		message, ok := payload["message"].(string)
		if err != nil || !ok || strings.TrimSpace(message) == "" {
			t.Fatalf("machine error lacks a complete message: %q %v", got.stderr, err)
		}
	}
}
