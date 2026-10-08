package custom

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/openai/openai-cli/internal/tokenizer"
	"github.com/urfave/cli/v3"
)

func tokenizerTestRoot(input io.Reader, out io.Writer) *cli.Command {
	root := &cli.Command{Name: "openai", Reader: input, Writer: out, ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
		},
	}
	registerTokenizerCommands(root)
	ConfigureCommandErrors(root)
	return root
}

type tokenizerRejectReader struct{}

func (tokenizerRejectReader) Read([]byte) (int, error) { panic("unexpected stdin read") }

func TestTokenizerSourcesPreserveExactBytes(t *testing.T) {
	for _, value := range []string{"", "Hello, world!", "a\r\nb\n", "\ufeffこんにちは 👩‍💻 e\u0301", "\x00\x1b\x7f\t", "@literal\n", "<|endoftext|>"} {
		t.Run(string([]rune(value)), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source with spaces.txt")
			if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, source := range []struct {
				name  string
				args  []string
				input io.Reader
			}{
				{"text", []string{"--text", value}, tokenizerRejectReader{}},
				{"file", []string{"--file", path}, tokenizerRejectReader{}},
				{"stdin", nil, strings.NewReader(value)},
				{"explicit stdin", []string{"--file", "-"}, strings.NewReader(value)},
			} {
				t.Run(source.name, func(t *testing.T) {
					var out bytes.Buffer
					root := tokenizerTestRoot(source.input, &out)
					args := append([]string{"openai", "--format", "json", "tokenizer", "inspect"}, source.args...)
					if err := root.Run(t.Context(), args); err != nil {
						t.Fatal(err)
					}
					var result struct {
						Encoding   string           `json:"encoding"`
						InputBytes int              `json:"input_bytes"`
						TokenCount int              `json:"token_count"`
						Tokens     []tokenizerToken `json:"tokens"`
					}
					if err := json.Unmarshal(out.Bytes(), &result); err != nil {
						t.Fatal(err)
					}
					var reconstructed []byte
					for _, token := range result.Tokens {
						fragment, err := hex.DecodeString(token.BytesHex)
						if err != nil || token.StartByte != len(reconstructed) || token.EndByte != token.StartByte+len(fragment) {
							t.Fatalf("invalid byte boundary: %+v; %v", token, err)
						}
						if utf8.Valid(fragment) {
							if token.Text == nil || *token.Text != string(fragment) {
								t.Fatalf("valid token text differs from bytes: %+v", token)
							}
						} else if token.Text != nil {
							t.Fatal("invalid UTF-8 fragment gained replacement text")
						}
						reconstructed = append(reconstructed, fragment...)
					}
					if string(reconstructed) != value || result.InputBytes != len(value) || result.TokenCount != len(result.Tokens) || result.Encoding != "o200k_base" {
						t.Fatalf("input changed: result=%+v; reconstructed=%q; input=%q", result, reconstructed, value)
					}
					if value == "" && !strings.Contains(out.String(), `"tokens":[]`) {
						t.Fatalf("empty tokens must be an array: %s", out.String())
					}
				})
			}
		})
	}
}

func TestTokenizerInvalidInputsDoNotReadStdin(t *testing.T) {
	for _, args := range [][]string{
		{"--text", "private text", "--file", "private path"},
		{"--encoding", "unknown-private-model"}, {"--encoding", "gpt-4o"},
		{"--file", ""}, {"--text", string([]byte{0xff})},
		{"--text", strings.Repeat("x", tokenizer.MaxInputBytes+1)},
		{"--text", "first", "--text", "second"}, {"--file", "first", "--file", "second"},
		{"--model", "gpt-4o"}, {"extra"},
	} {
		var out bytes.Buffer
		root := tokenizerTestRoot(tokenizerRejectReader{}, &out)
		err := root.Run(t.Context(), append([]string{"openai", "tokenizer", "count"}, args...))
		if err == nil || out.Len() != 0 {
			t.Fatalf("invalid input produced a result: error=%v, output=%q", err, out.String())
		}
		if strings.Contains(err.Error(), "unknown-private-model") || strings.Contains(err.Error(), "private text") {
			t.Fatalf("error echoed input: %v", err)
		}
	}
}

type tokenizerCountingReader struct{ bytes int }

func (r *tokenizerCountingReader) Read(data []byte) (int, error) {
	for i := range data {
		data[i] = 'x'
	}
	r.bytes += len(data)
	return len(data), nil
}

func TestTokenizerInputLimitBoundsReads(t *testing.T) {
	input := &tokenizerCountingReader{}
	var out bytes.Buffer
	root := tokenizerTestRoot(input, &out)
	err := root.Run(t.Context(), []string{"openai", "tokenizer", "count"})
	if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") || out.Len() != 0 {
		t.Fatalf("oversized input was accepted: %v; %q", err, out.String())
	}
	if input.bytes != tokenizer.MaxInputBytes+1 {
		t.Fatalf("read %d bytes; limit probe should read %d", input.bytes, tokenizer.MaxInputBytes+1)
	}
}

func TestTokenizerFormatsAndNoInputCommands(t *testing.T) {
	for _, format := range []string{"auto", "text", "json", "JSON"} {
		var previous string
		for i := 0; i < 2; i++ {
			var out bytes.Buffer
			root := tokenizerTestRoot(tokenizerRejectReader{}, &out)
			if err := root.Run(t.Context(), []string{"openai", "tokenizer", "count", "--text", "Hello, world!", "--format", format}); err != nil {
				t.Fatal(err)
			}
			if i > 0 && previous != out.String() {
				t.Fatal("repeated count output changed")
			}
			previous = out.String()
			if strings.EqualFold(format, "json") {
				if out.String() != "{\"encoding\":\"o200k_base\",\"input_bytes\":13,\"token_count\":4}\n" {
					t.Fatalf("unexpected JSON count: %s", out.String())
				}
			} else if out.String() != "Encoding: o200k_base\nInput bytes: 13\nTokens: 4\n" {
				t.Fatalf("unexpected text count: %q", out.String())
			}
		}
	}
	for _, subcommand := range []string{"encodings", "licenses"} {
		for _, format := range []string{"text", "json"} {
			var out bytes.Buffer
			root := tokenizerTestRoot(tokenizerRejectReader{}, &out)
			if err := root.Run(t.Context(), []string{"openai", "--format", format, "tokenizer", subcommand}); err != nil {
				t.Fatal(err)
			}
			if format == "json" && !json.Valid(out.Bytes()) {
				t.Fatalf("invalid %s JSON: %s", subcommand, out.String())
			}
			if subcommand == "licenses" && (!strings.Contains(out.String(), "Microsoft Corporation") || !strings.Contains(out.String(), "OpenAI")) {
				t.Fatal("bundled notices missing")
			}
		}
	}
	for _, args := range [][]string{{"--format", "explore"}, {"--format", "yaml"}, {"--format", "jsonl"}, {"--transform", "token_count"}, {"--raw-output"}} {
		var out bytes.Buffer
		root := tokenizerTestRoot(tokenizerRejectReader{}, &out)
		argv := append([]string{"openai"}, args...)
		err := root.Run(t.Context(), append(argv, "tokenizer", "count"))
		if err == nil || out.Len() != 0 {
			t.Fatalf("unsupported output accepted: %v; %s", err, out.String())
		}
	}
}

func TestTokenizerTerminalFragmentsAreEscaped(t *testing.T) {
	var out bytes.Buffer
	root := tokenizerTestRoot(tokenizerRejectReader{}, &out)
	if err := root.Run(t.Context(), []string{"openai", "tokenizer", "inspect", "--text", "\x1b[31m\x00\u202e\n👩"}); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{"\x1b", "\x00", "\u202e", "�"} {
		if strings.Contains(out.String(), unsafe) {
			t.Fatalf("unsafe or lossy fragment %q in output: %q", unsafe, out.String())
		}
	}
	if !strings.Contains(out.String(), "hex: 1b") {
		t.Fatal("lossless bytes missing")
	}
}

func TestTokenizerWriteFailureAndCancellation(t *testing.T) {
	failure := errors.New("private synthetic sink failure")
	for _, format := range []string{"text", "json"} {
		root := tokenizerTestRoot(tokenizerRejectReader{}, codexFailedWriter{failure})
		err := root.Run(t.Context(), []string{"openai", "--format", format, "tokenizer", "inspect", "--text", "Hello"})
		if !errors.Is(err, failure) || strings.Contains(err.Error(), "private synthetic") {
			t.Fatalf("writer failure lost or exposed: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	root := tokenizerTestRoot(tokenizerRejectReader{}, io.Discard)
	if err := root.Run(ctx, []string{"openai", "tokenizer", "count"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled command: %v", err)
	}
}
