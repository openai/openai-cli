package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func examplesTestRoot(out io.Writer) *cli.Command {
	root := &cli.Command{
		Name: "openai", Reader: tokenizerRejectReader{}, Writer: out, ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"},
			&cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
		},
	}
	registerExamplesCommands(root)
	return root
}

func TestExamplesCompletionFormats(t *testing.T) {
	command := examplesTestRoot(io.Discard).Command("examples")
	for _, target := range append([]*cli.Command{command}, command.Commands...) {
		values, ok := target.Metadata["completion-root-flag-values"].(map[string][]string)
		if !ok || len(values) != 1 || !slices.Equal(values["format"], []string{"auto", "text", "json"}) {
			t.Errorf("%s declares unsupported format completions: %#v", target.Name, values)
		}
	}
}

func TestExamplesFormatsPreserveScript(t *testing.T) {
	for _, topic := range []string{"files", "audio", "models"} {
		t.Run(topic, func(t *testing.T) {
			var plain strings.Builder
			if err := examplesTestRoot(&plain).Run(t.Context(), []string{"openai", "examples", topic}); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(plain.String(), "# POSIX shell.") || strings.ContainsRune(plain.String(), '\x1b') {
				t.Fatalf("recipe must be plain shell source: %q", plain.String())
			}
			for _, format := range []string{"auto", "text", "json", "JSON"} {
				var out strings.Builder
				if err := examplesTestRoot(&out).Run(t.Context(), []string{"openai", "--format", format, "examples", topic}); err != nil {
					t.Fatal(err)
				}
				if strings.EqualFold(format, "json") {
					var got workflowExample
					if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
						t.Fatal(err)
					}
					if got.Topic != topic || got.Shell != "sh" || got.Script != plain.String() {
						t.Fatalf("JSON changed recipe: %+v", got)
					}
				} else if out.String() != plain.String() {
					t.Fatalf("format changed recipe bytes: %q", out.String())
				}
			}
		})
	}
}

func TestExamplesRejectUnsupportedInput(t *testing.T) {
	for _, args := range [][]string{
		{"examples", "private-topic\x1b"}, {"examples", "files", "private-path"},
		{"--format", "json", "examples"},
		{"--format", "yaml", "examples", "files"}, {"--format", "jsonl", "examples", "audio"},
		{"--format", "raw", "examples", "models"}, {"--format", "pretty", "examples", "files"},
		{"--format", "explore", "examples", "files"},
		{"--transform", "private-field", "examples", "files"},
		{"--transform=", "examples", "files"}, {"--raw-output=false", "examples", "files"},
	} {
		var out strings.Builder
		err := examplesTestRoot(&out).Run(t.Context(), append([]string{"openai"}, args...))
		if err == nil || out.Len() != 0 || strings.Contains(err.Error(), "private-") {
			t.Errorf("args %q: output=%q, error=%v", args, out.String(), err)
		}
	}
}

type examplesFailWriter struct {
	err error
}

func (w examplesFailWriter) Write(data []byte) (int, error) { return len(data) / 2, w.err }

func TestExamplesOutputFailures(t *testing.T) {
	failure := errors.New("synthetic write failure")
	for _, args := range [][]string{{"examples"}, {"--format", "text", "examples", "files"}, {"--format", "json", "examples", "files"}} {
		for _, cause := range []error{failure, nil} {
			err := examplesTestRoot(examplesFailWriter{cause}).Run(t.Context(), append([]string{"openai"}, args...))
			want := cause
			if want == nil {
				want = io.ErrShortWrite
			}
			if !errors.Is(err, want) || !strings.Contains(err.Error(), "Output may be incomplete") {
				t.Fatalf("args=%q: lost output failure: %v", args, err)
			}
		}
	}
}

func TestExamplesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, args := range [][]string{{"examples"}, {"examples", "files"}} {
		var out strings.Builder
		err := examplesTestRoot(&out).Run(ctx, append([]string{"openai"}, args...))
		if !errors.Is(err, context.Canceled) || out.Len() != 0 {
			t.Fatalf("cancellation lost: output=%q, error=%v", out.String(), err)
		}
	}
}

func TestExamplesSuppressFirstRunSetup(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		for _, args := range [][]string{
			{"openai", "examples"}, {"openai", "examples", "files"},
			{"openai", "--format", "json", "examples", "audio"},
			{"openai", "help", "examples", "models"},
		} {
			if imagePickerFirstRunEligible(args, func(string) string { return "" }, true, true, true, shell) {
				t.Errorf("%s allowed first-run setup for %q", shell, args)
			}
		}
	}
}
