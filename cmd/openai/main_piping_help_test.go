package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func TestMainPipingHelp(t *testing.T) {
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	const sentinel = "synthetic unread stdin"
	if _, err := io.WriteString(writer, sentinel); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	env := []string{"OPENAI_BASE_URL=invalid-for-requests", "OPENAI_MTLS_CLIENT_CERT_FILE=/missing/synthetic.pem"}
	for _, args := range [][]string{{"openai", "help", "piping"}, {"openai", "help", "piping", "--help"}} {
		result := runMainDispatchWithStdin(t, "bash", env, input, args...)
		if result.code != 0 || result.stderr != "" {
			t.Fatalf("%q failed: %+v", args, result)
		}
		for _, example := range []string{
			"printf hello | openai tokenizer view",
			"printf hello | openai tokenizer inspect --format json",
			"printf hello | openai tokenizer count -n",
			"count --transform input_bytes",
			"openai responses create --model MODEL --input @- < prompt.txt",
			"openai files create --purpose user_data --file - < notes.txt",
			"chat completions create",
			"cat request.json | openai responses create --request-file -",
			"openai embeddings create < request.json",
			"openai models list --format jsonl",
		} {
			if !strings.Contains(result.stdout, example) {
				t.Errorf("%q omitted %q", args, example)
			}
		}
	}
	got, err := io.ReadAll(input)
	if err != nil || string(got) != sentinel {
		t.Fatalf("help consumed stdin, got %q, err %v", got, err)
	}

	result := runMainDispatchWithEnv(t, "bash", env, "openai", "help", "piping", "synthetic-private-operand")
	if result.code != 3 || result.stdout != "" || !strings.Contains(result.stderr, "Unknown help topic") || strings.Contains(result.stderr, "synthetic-private-operand") {
		t.Fatalf("unexpected extra-argument result: %+v", result)
	}
}
