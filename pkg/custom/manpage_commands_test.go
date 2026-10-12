package custom

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func manpageTestCommand(t *testing.T, dir string) (*cli.Command, *cli.Command) {
	t.Helper()
	flag := &cli.StringFlag{Name: "filter", Usage: "Filter documented items"}
	leaf := &cli.Command{Name: "list", Aliases: []string{"ls"}, Usage: "List documented items", Flags: []cli.Flag{flag}}
	group := &cli.Command{Name: "resources", Commands: []*cli.Command{leaf}}
	hidden := &cli.Command{Name: "internal", Hidden: true, Commands: []*cli.Command{{Name: "secret"}}}
	manpages := &cli.Command{
		Name: "@manpages", Hidden: true, HideHelpCommand: true,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "output", Value: dir},
			&cli.BoolFlag{Name: "text", Value: true},
			&cli.BoolFlag{Name: "gzip", Value: true},
		},
		Action: func(context.Context, *cli.Command) error {
			return errors.New("the generated action must not run")
		},
	}
	root := &cli.Command{
		Name: "openai", HideHelpCommand: true,
		Flags:    []cli.Flag{&cli.BoolFlag{Name: "quiet", Value: true}},
		Commands: []*cli.Command{group, hidden, manpages},
	}
	configureManpageCommands(root)
	return root, manpages
}

func TestManpageCommandsPreserveRuntimeTree(t *testing.T) {
	dir := t.TempDir()
	root, manpages := manpageTestCommand(t, dir)
	original := slices.Clone(root.Commands)
	group := original[0]
	leaf := group.Commands[0]
	flag := leaf.Flags[0]
	assertTree := func() {
		t.Helper()
		if !slices.Equal(root.Commands, original) || group.Commands[0] != leaf || leaf.Name != "list" || !slices.Equal(leaf.Aliases, []string{"ls"}) || leaf.Flags[0] != flag {
			t.Fatal("documentation generation changed the runtime command tree")
		}
	}
	if err := root.Run(context.Background(), []string{"openai", "@manpages"}); err != nil {
		t.Fatal(err)
	}
	assertTree()
	text, err := os.ReadFile(filepath.Join(dir, "man1", "openai.1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".SH resources list, resources ls", "List documented items", "Filter documented items"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("manpage lost %q", want)
		}
	}
	if strings.Contains(string(text), "internal") || strings.Contains(string(text), "secret") {
		t.Error("hidden subtree exposed")
	}
	gzipped, err := os.ReadFile(filepath.Join(dir, "man1", "openai.1.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gzipped, manpageTestGzip(t, text)) {
		t.Fatal("gzip output changed from a single write with default gzip headers")
	}
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := manpages.Set("output", blocked); err != nil {
		t.Fatal(err)
	}
	if err := manpages.Action(context.Background(), manpages); err == nil {
		t.Fatal("directory failure returned success")
	}
	assertTree()
	if err := manpages.Set("output", dir); err != nil {
		t.Fatal(err)
	}
	if err := manpages.Action(context.Background(), manpages); err != nil {
		t.Fatal(err)
	}
	assertTree()
	for name, want := range map[string][]byte{"openai.1": text, "openai.1.gz": gzipped} {
		got, err := os.ReadFile(filepath.Join(dir, "man1", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("repeated %s changed: %v", name, err)
		}
	}
}

func TestManpageCommandsDisabledFormats(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory created", true: "directory failure"}[blocked], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "output")
			if blocked {
				if err := os.WriteFile(dir, []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			root, _ := manpageTestCommand(t, dir)
			err := root.Run(context.Background(), []string{"openai", "@manpages", "--text=false", "--gzip=false"})
			if blocked {
				if err == nil {
					t.Fatal("disabled formats hid directory failure")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(filepath.Join(dir, "man1"))
			if err != nil || len(files) != 0 {
				t.Fatalf("disabled formats must create an empty directory: files=%v, err=%v", files, err)
			}
		})
	}
}

func TestManpageCommandsKeepTextAfterGzipCreateFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "man1", "openai.1.gz"), 0755); err != nil {
		t.Fatal(err)
	}
	root, manpages := manpageTestCommand(t, dir)
	if err := root.Run(context.Background(), []string{"openai", "@manpages"}); err == nil {
		t.Fatal("gzip create failure returned success")
	}
	text, err := os.ReadFile(filepath.Join(dir, "man1", "openai.1"))
	if err != nil || len(text) == 0 {
		t.Fatalf("completed text was not retained: %v", err)
	}
	if err := manpages.Set("gzip", "false"); err != nil {
		t.Fatal(err)
	}
	if err := manpages.Action(context.Background(), manpages); err != nil {
		t.Fatal(err)
	}
	complete, err := os.ReadFile(filepath.Join(dir, "man1", "openai.1"))
	if err != nil || !bytes.Equal(text, complete) {
		t.Fatalf("partial failure did not retain the complete text: %v", err)
	}
}

func manpageTestGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

type manpageTestFile struct {
	write            func([]byte) (int, error)
	closeErr         error
	closeCalls       int
	writesAfterClose int
}

func (file *manpageTestFile) Write(data []byte) (int, error) {
	if file.closeCalls != 0 {
		file.writesAfterClose++
	}
	return file.write(data)
}

func (file *manpageTestFile) Close() error {
	file.closeCalls++
	return file.closeErr
}

func TestWriteManpageFileErrorsAndCleanup(t *testing.T) {
	const manpage = "synthetic manpage\n"
	writeErr := errors.New("synthetic write failure")
	closeErr := errors.New("synthetic file-close failure")
	gzipSize := len(manpageTestGzip(t, []byte(manpage)))
	for _, tc := range []struct {
		name       string
		compressed bool
		limit      int
		writeErr   error
		closeErr   error
		wantErr    error
	}{
		{name: "text success", limit: -1},
		{name: "text short write", limit: 2, wantErr: io.ErrShortWrite},
		{name: "text write failure", limit: 2, writeErr: writeErr, wantErr: writeErr},
		{name: "text close failure", limit: -1, closeErr: closeErr},
		{name: "text joined failures", limit: 2, writeErr: writeErr, closeErr: closeErr, wantErr: writeErr},
		{name: "gzip success", compressed: true, limit: -1},
		{name: "gzip close failure", compressed: true, limit: -1, closeErr: closeErr},
		{name: "gzip header failure", compressed: true, limit: 0, writeErr: writeErr, closeErr: closeErr, wantErr: writeErr},
		{name: "gzip finalization failure", compressed: true, limit: 10, writeErr: writeErr, closeErr: closeErr, wantErr: writeErr},
		{name: "gzip footer failure", compressed: true, limit: gzipSize - 8, writeErr: writeErr, closeErr: closeErr, wantErr: writeErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			file := &manpageTestFile{closeErr: tc.closeErr, write: func(data []byte) (int, error) {
				if tc.limit >= 0 && len(data) > tc.limit-output.Len() {
					n, err := output.Write(data[:tc.limit-output.Len()])
					if err != nil {
						return n, err
					}
					return n, tc.writeErr
				}
				return output.Write(data)
			}}
			err := writeManpageFile(file, manpage, tc.compressed)
			if tc.wantErr == nil && tc.closeErr == nil && err != nil {
				t.Fatal(err)
			}
			for _, want := range []error{tc.wantErr, tc.closeErr} {
				if want != nil && !errors.Is(err, want) {
					t.Fatalf("error %v lost %v", err, want)
				}
			}
			if file.closeCalls != 1 || file.writesAfterClose != 0 {
				t.Fatalf("file closes=%d, writes after close=%d", file.closeCalls, file.writesAfterClose)
			}
			if tc.limit >= 0 && output.Len() != tc.limit {
				t.Fatalf("failure boundary: wrote %d bytes, want %d", output.Len(), tc.limit)
			}
			if tc.limit < 0 {
				want := []byte(manpage)
				if tc.compressed {
					want = manpageTestGzip(t, want)
				}
				if !bytes.Equal(output.Bytes(), want) {
					t.Fatal("file closed before all content was written")
				}
			}
		})
	}
}
