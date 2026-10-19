//go:build !windows

package custom

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	sockettest "github.com/openai/openai-cli/internal/testutil/socket"
)

func TestTokenizerSocketCancellationAndReuse(t *testing.T) {
	for _, blocking := range []bool{false, true} {
		name := "nonblocking"
		if blocking {
			name = "blocking"
		}
		for _, source := range []string{"stdin", "file-dash"} {
			t.Run(name+"/"+source, func(t *testing.T) {
				input, producer := sockettest.Pair(t, blocking)
				flags := sockettest.Flags(t, input)
				args := []string{"openai", "--format", "json", "tokenizer", "inspect"}
				if source == "file-dash" {
					args = append(args, "--file", "-")
				}
				var out bytes.Buffer
				ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
				done := make(chan error, 1)
				go func() { done <- tokenizerTestRoot(input, &out).Run(ctx, args) }()
				err := sockettest.AwaitRead(t, producer, done)
				cancel()
				if !errors.Is(err, context.DeadlineExceeded) || out.Len() != 0 || sockettest.Flags(t, input) != flags {
					t.Fatalf("Tokenizer socket cancellation changed: %v / %q", err, out.String())
				}
				const value = "\ufeffhello\r\n👩\u200d💻 e\u0301\x00\x1b\t\n"
				if _, err := io.WriteString(producer, value); err != nil {
					t.Fatal(err)
				}
				producer.Close()
				if err := tokenizerTestRoot(input, &out).Run(t.Context(), args); err != nil {
					t.Fatal(err)
				}
				var result struct {
					InputBytes int `json:"input_bytes"`
					Tokens     []struct {
						StartByte int    `json:"start_byte"`
						EndByte   int    `json:"end_byte"`
						BytesHex  string `json:"bytes_hex"`
					} `json:"tokens"`
				}
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				var reconstructed []byte
				for _, token := range result.Tokens {
					fragment, err := hex.DecodeString(token.BytesHex)
					if err != nil || token.StartByte != len(reconstructed) || token.EndByte != len(reconstructed)+len(fragment) {
						t.Fatalf("Tokenizer socket changed token bytes: %+v / %v", token, err)
					}
					reconstructed = append(reconstructed, fragment...)
				}
				if string(reconstructed) != value || result.InputBytes != len(value) || sockettest.Flags(t, input) != flags {
					t.Fatal("Tokenizer socket data or descriptor flags changed")
				}
				if _, err := input.Read(make([]byte, 1)); err != io.EOF {
					t.Fatalf("Tokenizer closed its borrowed input or lost EOF: %v", err)
				}
			})
		}
	}
}
