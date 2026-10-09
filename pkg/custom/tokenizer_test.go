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
	"reflect"
	"strings"
	"testing"
	"time"
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
		{"--encoding", "gpt2"}, {"--encoding", "p50k_edit"}, {"--encoding", "P50K_BASE"}, {"--encoding", "r50k_base "},
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

func TestTokenizerLegacyEncodingSources(t *testing.T) {
	text := "  "
	path := filepath.Join(t.TempDir(), "legacy input.txt")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		ids  []uint
	}{
		{"r50k_base", []uint{220, 220}},
		{"p50k_base", []uint{50257}},
	} {
		for _, source := range []string{"text", "file", "stdin", "explicit stdin"} {
			for _, operation := range []string{"count", "inspect"} {
				t.Run(tc.name+"/"+source+"/"+operation, func(t *testing.T) {
					var input io.Reader = tokenizerRejectReader{}
					args := []string{"openai", "--format", "json", "tokenizer", operation, "--encoding", tc.name}
					switch source {
					case "text":
						args = append(args, "--text", text)
					case "file":
						args = append(args, "--file", path)
					case "stdin":
						input = strings.NewReader(text)
					case "explicit stdin":
						args = append(args, "--file", "-")
						input = strings.NewReader(text)
					}
					var out bytes.Buffer
					if err := tokenizerTestRoot(input, &out).Run(t.Context(), args); err != nil {
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
					if result.Encoding != tc.name || result.InputBytes != len(text) || result.TokenCount != len(tc.ids) {
						t.Fatalf("legacy count changed: %s", out.String())
					}
					if operation == "inspect" {
						var ids []uint
						var data []byte
						for _, token := range result.Tokens {
							fragment, err := hex.DecodeString(token.BytesHex)
							if err != nil || token.StartByte != len(data) || token.EndByte != len(data)+len(fragment) {
								t.Fatalf("invalid legacy byte boundaries: %+v", token)
							}
							data = append(data, fragment...)
							ids = append(ids, token.ID)
						}
						if !reflect.DeepEqual(ids, tc.ids) || string(data) != text {
							t.Fatalf("legacy result changed: %s", out.String())
						}
					} else if result.Tokens != nil {
						t.Fatal("count unexpectedly returned inspection data")
					}
				})
			}
		}
	}
}

func TestTokenizerEncodingListPreservesExactNames(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		var out bytes.Buffer
		if err := tokenizerTestRoot(tokenizerRejectReader{}, &out).Run(t.Context(), []string{"openai", "tokenizer", "encodings", "--format", format}); err != nil {
			t.Fatal(err)
		}
		want := "o200k_base (default)\ncl100k_base\nr50k_base (legacy)\np50k_base (legacy)\n"
		if format == "json" {
			want = "{\"default_encoding\":\"o200k_base\",\"encodings\":[\"o200k_base\",\"cl100k_base\",\"r50k_base\",\"p50k_base\"]}\n"
		}
		if out.String() != want {
			t.Fatalf("encoding list = %q; want %q", out.String(), want)
		}
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

type tokenizerCooperativeReader struct {
	read func(context.Context, []byte) (int, error)
}

func (*tokenizerCooperativeReader) Read([]byte) (int, error) {
	panic("context-aware input used synchronous Read")
}

func (r *tokenizerCooperativeReader) ReadContext(ctx context.Context, data []byte) (int, error) {
	return r.read(ctx, data)
}

func (*tokenizerCooperativeReader) Close() error { panic("caller input was closed") }

func TestTokenizerCooperativeInputPreservesBytes(t *testing.T) {
	value := "\ufeffa\r\n👩\u200d💻 e\u0301\x00\x1b\t\n"
	for _, mode := range []string{"count", "inspect"} {
		for _, format := range []string{"text", "json"} {
			t.Run(mode+"/"+format, func(t *testing.T) {
				reader := strings.NewReader(value)
				input := &tokenizerCooperativeReader{read: func(_ context.Context, data []byte) (int, error) {
					return reader.Read(data)
				}}
				args := []string{"openai", "--format", format, "tokenizer", mode}
				var got, want bytes.Buffer
				if err := tokenizerTestRoot(input, &got).Run(t.Context(), args); err != nil {
					t.Fatal(err)
				}
				literal := append(append([]string(nil), args...), "--text", value)
				if err := tokenizerTestRoot(tokenizerRejectReader{}, &want).Run(t.Context(), literal); err != nil {
					t.Fatal(err)
				}
				if got.String() != want.String() {
					t.Fatalf("cooperative input changed output: got %q; want %q", got.String(), want.String())
				}
			})
		}
	}
}

func TestTokenizerCooperativeInputCancellation(t *testing.T) {
	for _, mode := range []string{"count", "inspect"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			input := &tokenizerCooperativeReader{read: func(ctx context.Context, _ []byte) (int, error) {
				close(started)
				<-ctx.Done()
				return 0, ctx.Err()
			}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var out bytes.Buffer
			done := make(chan error, 1)
			go func() {
				done <- tokenizerTestRoot(input, &out).Run(ctx, []string{"openai", "tokenizer", mode, "--file", "-"})
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("command did not enter cooperative ReadContext")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || out.Len() != 0 {
					t.Fatalf("cancellation changed: %v / %q", err, out.String())
				}
			case <-time.After(time.Second):
				t.Fatal("cooperative cancellation did not return")
			}
		})
	}
}

func TestTokenizerInputReadErrorsAndCanceledEOF(t *testing.T) {
	failure := errors.New("private synthetic input failure")
	for _, operation := range []string{"read error", "canceled EOF"} {
		t.Run(operation, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			input := &tokenizerCooperativeReader{read: func(_ context.Context, data []byte) (int, error) {
				n := copy(data, "synthetic input")
				if operation == "canceled EOF" {
					cancel()
					return n, io.EOF
				}
				return n, failure
			}}
			var out bytes.Buffer
			err := tokenizerTestRoot(input, &out).Run(ctx, []string{"openai", "--format", "json", "tokenizer", "inspect"})
			want := failure
			if operation == "canceled EOF" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || strings.Contains(err.Error(), "private synthetic") || out.Len() != 0 {
				t.Fatalf("read error changed or escaped: %v / %q", err, out.String())
			}
		})
	}
}

func TestTokenizerBorrowedRegularFileRemainsOpen(t *testing.T) {
	value := "\ufeffexact\r\nbytes 👩\u200d💻\n"
	path := filepath.Join(t.TempDir(), "synthetic.txt")
	if err := os.WriteFile(path, []byte(value), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	for range 3 {
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		if err := tokenizerTestRoot(input, io.Discard).Run(t.Context(), []string{"openai", "tokenizer", "count"}); err != nil {
			t.Fatal(err)
		}
		if _, err := input.Stat(); err != nil {
			t.Fatalf("caller file was closed: %v", err)
		}
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(input)
	if err != nil || string(data) != value {
		t.Fatalf("caller file changed: %q / %v", data, err)
	}
}
