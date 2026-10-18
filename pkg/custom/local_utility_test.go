package custom

import (
	"bytes"
	"github.com/openai/openai-cli/internal/tokenizer"
	"io"
	"strings"
	"testing"

	"github.com/openai/openai-cli/internal/commands/tokenize"
	"github.com/urfave/cli/v3"
)

func tokenizerTestRoot(input io.Reader, out io.Writer) *cli.Command {
	root := &cli.Command{Name: "openai", Reader: input, Writer: out, ErrWriter: io.Discard,
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "format", Value: "auto"}, &cli.StringFlag{Name: "transform"},
			&cli.BoolFlag{Name: "raw-output"},
		},
	}
	tokenize.Register(root)
	ConfigureCommandErrors(root)
	return root
}

type tokenizerRejectReader struct{}

func (tokenizerRejectReader) Read([]byte) (int, error) { panic("unexpected stdin read") }

// Invalid flag output is an integration contract with the shared error policy.
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
