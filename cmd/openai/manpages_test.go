package main

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMainManpagesDeepSubgroups(t *testing.T) {
	dir := t.TempDir()
	got := runMainDispatch(t, "bash", "openai", "@manpages", "--text", "-o", dir)
	if got.code != 0 || got.stderr != "" {
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
	} {
		if !strings.Contains(string(text), "\n.SH "+path+"\n") {
			t.Errorf("missing full-path heading for %s", path)
		}
	}
	for _, hidden := range []string{"@manpages", "__complete", "admin:organization", "\n.SH admin organization", "\n.SH audio transcriptions"} {
		if strings.Contains(string(text), hidden) {
			t.Errorf("hidden command %s leaked into manpage", hidden)
		}
	}
}
