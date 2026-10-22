//go:build !windows

package commandsuggest

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateSinkOwnsOnlyDuplicate(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "commands-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	sink := privateSink(int(file.Fd()))
	if sink == nil {
		t.Fatal("private file rejected")
	}
	if _, err := io.WriteString(sink, "openai models list\n"); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Stat(); err != nil {
		t.Fatalf("caller FD closed: %v", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(file)
	if err != nil || string(data) != "openai models list\n" {
		t.Fatalf("data=%q error=%v", data, err)
	}
}

func TestPrivateSinkRejectsUnsafeDescriptors(t *testing.T) {
	t.Run("read-only", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "capture")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		if sink := privateSink(int(file.Fd())); sink != nil {
			sink.Close()
			t.Fatal("read-only descriptor accepted")
		}
		if _, err := file.Stat(); err != nil {
			t.Fatalf("caller FD closed: %v", err)
		}
	})
	for _, tc := range []struct {
		name     string
		mode     os.FileMode
		contents string
	}{
		{"public", 0644, ""}, {"occupied", 0600, "existing data"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file, err := os.OpenFile(filepath.Join(t.TempDir(), "sink"), os.O_RDWR|os.O_CREATE|os.O_EXCL, tc.mode)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if _, err := io.WriteString(file, tc.contents); err != nil {
				t.Fatal(err)
			}
			if sink := privateSink(int(file.Fd())); sink != nil {
				sink.Close()
				t.Fatal("unsafe descriptor accepted")
			}
			if _, err := file.Stat(); err != nil {
				t.Fatalf("caller FD closed: %v", err)
			}
		})
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if sink := privateSink(int(writer.Fd())); sink != nil {
		sink.Close()
		t.Fatal("pipe accepted")
	}
	if sink := privateSink(-1); sink != nil {
		sink.Close()
		t.Fatal("invalid descriptor accepted")
	}
}

func TestConfigureIgnoresInvalidEnvironment(t *testing.T) {
	for _, tc := range []struct{ shell, fd string }{{"unknown", "9"}, {"zsh", "bad"}, {"zsh", "1"}, {"zsh", "99999"}} {
		t.Setenv("OPENAI_CLI_COMMANDS_SHELL", tc.shell)
		t.Setenv("OPENAI_CLI_COMMANDS_FD", tc.fd)
		Configure(nil)()
	}
}
