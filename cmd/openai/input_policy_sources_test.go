package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inputPolicySource(t *testing.T, source, value string) (string, *os.File) {
	t.Helper()
	switch source {
	case "file":
		path := filepath.Join(t.TempDir(), "operand.yaml")
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		return "@" + path, nil
	case "stdin":
		return "@-", shellFileInput(t, []byte(value))
	default:
		return value, nil
	}
}

func TestMainInputPolicyStructuredSources(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, tc := range []struct {
		name, value string
		want        any
	}{
		{"string", `"  text: # preserved  "`, "  text: # preserved  "},
		{"number", "42", float64(42)},
		{"boolean", "true", true},
		{"null", "null", nil},
		{"JSON list", `["one","two"]`, []any{"one", "two"}},
		{"YAML list", "- one\n- two\n", []any{"one", "two"}},
		{"JSON object", `{"role":"user","content":"synthetic"}`, map[string]any{"role": "user", "content": "synthetic"}},
		{"YAML object", "role: user\ncontent: synthetic\n", map[string]any{"role": "user", "content": "synthetic"}},
	} {
		for _, source := range []string{"flag", "file", "stdin"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				operand, stdin := inputPolicySource(t, source, tc.value)
				got := runShellFileCommand(t, server, stdin, nil, "responses", "create", "--model", "model_synthetic", "--input-structured", operand)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("command: %+v", got)
				}
				inputPolicyValue(t, inputPolicyBody(t, requests), "input", tc.want)
			})
		}
	}
}

func TestMainInputPolicySourcesResolveOnce(t *testing.T) {
	server, requests := inputPolicyServer(t)
	privatePath := filepath.Join(t.TempDir(), "synthetic-private.txt")
	if err := os.WriteFile(privatePath, []byte("synthetic-private-contents"), 0600); err != nil {
		t.Fatal(err)
	}
	reference := "@" + privatePath
	encodedReference, err := json.Marshal(reference)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, flag, value string
		want              any
	}{
		{"literal file reference", "input", reference, reference},
		{"structured string", "input-structured", string(encodedReference), reference},
		{"structured nested reference", "input-structured", `[{"role":"user","content":` + string(encodedReference) + `}]`, []any{map[string]any{"role": "user", "content": reference}}},
		{"structured stdin reference", "input-structured", `"@-"`, "@-"},
	} {
		for _, source := range []string{"file", "stdin"} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				operand, stdin := inputPolicySource(t, source, tc.value)
				got := runShellFileCommand(t, server, stdin, nil, "responses", "create", "--model", "model_synthetic", "--"+tc.flag, operand)
				if got.code != 0 || got.stderr != "" {
					t.Fatalf("command: %+v", got)
				}
				inputPolicyValue(t, inputPolicyBody(t, requests), "input", tc.want)
			})
		}
	}
	for _, tc := range []struct{ flag, value string }{
		{"input", `\` + reference},
		{"input-structured", string(encodedReference)},
	} {
		t.Run(tc.flag+"/inline", func(t *testing.T) {
			got := runShellFileCommand(t, server, nil, nil, "responses", "create", "--model", "model_synthetic", "--"+tc.flag, tc.value)
			if got.code != 0 || got.stderr != "" {
				t.Fatalf("command: %+v", got)
			}
			inputPolicyValue(t, inputPolicyBody(t, requests), "input", reference)
		})
	}
}

func TestMainInputPolicyMalformedStructuredSources(t *testing.T) {
	server, requests := inputPolicyServer(t)
	const privateValue = "[synthetic-private-invalid\x1b[31m"
	for _, source := range []string{"flag", "file", "stdin"} {
		for _, format := range []string{"text", "json"} {
			t.Run(source+"/"+format, func(t *testing.T) {
				operand, stdin := inputPolicySource(t, source, privateValue)
				got := runShellFileCommand(t, server, stdin, nil, "--format-error", format, "responses", "create", "--model", "model_synthetic", "--input-structured", operand)
				if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "input-structured") {
					t.Fatalf("malformed source did not fail locally: %+v", got)
				}
				if strings.Contains(got.stderr, "synthetic-private-invalid") || strings.Contains(got.stderr, "\x1b") || (source == "file" && strings.Contains(got.stderr, operand[1:])) {
					t.Fatalf("diagnostic disclosed structured input: %q", got.stderr)
				}
				if format == "json" && !json.Valid([]byte(got.stderr)) {
					t.Fatalf("structured diagnostic is not JSON: %q", got.stderr)
				}
				inputPolicyNoRequest(t, requests)
			})
		}
	}
}

func TestMainInputPolicyConflictPrecedesSources(t *testing.T) {
	server, requests := inputPolicyServer(t)
	for _, mode := range []string{"literal", "legacy"} {
		for _, structuredFirst := range []bool{false, true} {
			for _, source := range []string{"missing file", "stdin"} {
				t.Run(mode+"/"+source+"/structured-first="+map[bool]string{false: "false", true: "true"}[structuredFirst], func(t *testing.T) {
					operand := "@" + filepath.Join(t.TempDir(), "synthetic-private-missing.txt")
					var stdin *os.File
					if source == "stdin" {
						operand = "@-"
						stdin = shellFileInput(t, []byte(`{"input":"synthetic whole request"}`))
					}
					flags := []string{"--input", operand, "--input-structured", operand}
					if structuredFirst {
						flags = []string{"--input-structured", operand, "--input", operand}
					}
					args := append([]string{"responses", "create", "--model", "model_synthetic"}, flags...)
					got := runShellFileCommand(t, server, stdin, []string{"OPENAI_CLI_INPUT_MODE=" + mode}, args...)
					if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "Use either --input or --input-structured") {
						t.Fatalf("conflict did not precede source handling: %+v", got)
					}
					if stdin != nil {
						position, err := stdin.Seek(0, io.SeekCurrent)
						if err != nil || position != 0 {
							t.Fatalf("conflict consumed stdin: offset %d, error %v", position, err)
						}
					}
					inputPolicyNoRequest(t, requests)
				})
			}
		}
	}
}
