package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainDispatchOutputLocalUtilityDataAndDiagnostics(t *testing.T) {
	server, requests := localUtilitiesRequestTrap(t)
	for _, tc := range []struct {
		label, format, contains string
		args                    []string
	}{
		{"tokenizer count", "text", "Tokens: 4\n", []string{"tokenizer", "count", "--text", "Hello, world!"}},
		{"tokenizer inspect", "json", `"token_count":4`, []string{"tokenizer", "inspect", "--text", "Hello, world!"}},
		{"tokenizer encodings", "json", `"default_encoding"`, []string{"tokenizer", "encodings"}},
		{"tokenizer licenses", "text", "MIT License", []string{"tokenizer", "licenses"}},
		{"tokenizer", "auto", "Commands\n", []string{"tokenizer"}},
		{"codex", "json", `"command": "codex"`, []string{"codex"}},
		{"codex destination", "text", "https://learn.chatgpt.com/docs/config-file/config-basic", []string{"codex", "--destination", "config"}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL="+server.URL,
				"OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert", "OPENAI_MTLS_CLIENT_KEY_FILE=",
				"FORCE_COLOR=1", "CLICOLOR_FORCE=1")
			prefix := []string{"openai", "--format", tc.format, "--format-error", "text"}
			ordinary := runMainDispatchWithEnv(t, "bash", env, append(append([]string{}, prefix...), tc.args...)...)
			if ordinary.code != 0 || ordinary.stderr != "" || !strings.Contains(ordinary.stdout, tc.contains) ||
				strings.Contains(ordinary.stdout, "\x1b") || tc.format == "json" && !json.Valid([]byte(ordinary.stdout)) {
				t.Fatalf("local utility control failed: %+v", ordinary)
			}
			for _, policy := range []string{"quiet after command", "verbose before command", "quiet verbose trailing"} {
				t.Run(policy, func(t *testing.T) {
					args := append([]string{}, prefix...)
					switch policy {
					case "quiet after command":
						args = append(args, tc.args[0], "--quiet")
						args = append(args, tc.args[1:]...)
					case "verbose before command":
						args = append(args, "--verbose")
						args = append(args, tc.args...)
					default:
						args = append(args, tc.args...)
						args = append(args, "--quiet", "--verbose")
					}
					got := runMainDispatchWithEnv(t, "bash", env, args...)
					if got.code != ordinary.code || got.stdout != ordinary.stdout {
						t.Fatalf("feedback policy changed selected data: ordinary=%+v got=%+v", ordinary, got)
					}
					if policy == "verbose before command" {
						label := tc.label
						if tc.args[0] == "codex" {
							label = "codex"
						}
						assertLocalUtilityVerbose(t, got.stderr, label, tc.format, "completed", "")
					} else if got.stderr != "" {
						t.Fatalf("quiet emitted optional feedback: %+v", got)
					}
				})
			}
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("local utility reached the API: %d requests", requests.Load())
	}
}

func TestMainDispatchOutputLocalUtilityMachineGuards(t *testing.T) {
	for _, command := range [][]string{{"tokenizer", "count", "--text", "synthetic-private-input"}, {"codex", "--destination", "docs"}} {
		for _, flags := range [][]string{
			{"--format", "json"},
			{"--format-error", "json"},
			{"--format-error", "text", "--transform-error", "message"},
		} {
			t.Run(command[0]+"/"+strings.Join(flags, " "), func(t *testing.T) {
				env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL=://synthetic-private-endpoint",
					"OPENAI_CUSTOM_HEADERS=synthetic-private-headers", "OPENAI_MTLS_CLIENT_CERT_FILE=/synthetic-private/missing-cert")
				args := append([]string{"openai"}, flags...)
				args = append(args, command...)
				ordinary := runMainDispatchWithEnv(t, "bash", env, args...)
				got := runMainDispatchWithEnv(t, "bash", env, append(args, "--verbose")...)
				if ordinary.code != 0 || ordinary.stdout == "" || ordinary.stderr != "" || got != ordinary {
					t.Fatalf("machine guard changed offline local output: ordinary=%+v verbose=%+v", ordinary, got)
				}
				if flags[0] == "--format" && !json.Valid([]byte(got.stdout)) {
					t.Fatalf("local JSON output is invalid: %q", got.stdout)
				}
			})
		}
	}
}

func TestMainDispatchOutputLocalUtilityUnsupportedModes(t *testing.T) {
	for _, command := range [][]string{{"tokenizer", "count", "--text", "synthetic-private-input"}, {"codex"}} {
		for _, flags := range [][]string{
			{"--format", "jsonl"}, {"--format", "yaml"}, {"--format", "raw"},
			{"--format", "pretty"}, {"--format", "explore"},
			{"--transform", "synthetic-private-field"}, {"--raw-output=false"},
		} {
			t.Run(command[0]+"/"+strings.Join(flags, " "), func(t *testing.T) {
				env := localUtilitiesEnvironment(t)
				args := append([]string{"openai", "--format-error", "json"}, flags...)
				args = append(args, command...)
				ordinary := runMainDispatchWithEnv(t, "bash", env, args...)
				got := runMainDispatchWithEnv(t, "bash", env, append(args, "--verbose")...)
				var detail struct {
					Message string `json:"message"`
				}
				if ordinary.code != 1 || ordinary.stdout != "" || got != ordinary ||
					json.Unmarshal([]byte(got.stderr), &detail) != nil || detail.Message == "" ||
					strings.Contains(got.stderr, "synthetic-private-") {
					t.Fatalf("unsupported mode lost its complete machine error: ordinary=%+v verbose=%+v", ordinary, got)
				}
			})
		}
	}
}

func TestMainDispatchOutputLocalUtilityFailureDiagnostics(t *testing.T) {
	for _, command := range [][]string{
		{"tokenizer", "count", "--encoding", "synthetic-private-encoding", "--text", "synthetic-private-input"},
		{"codex", "--destination", "synthetic-private-destination"},
	} {
		t.Run(command[0], func(t *testing.T) {
			env := append(localUtilitiesEnvironment(t), "OPENAI_BASE_URL=://synthetic-private-endpoint")
			args := append([]string{"openai"}, command...)
			ordinary := runMainDispatchWithEnv(t, "bash", env, args...)
			if ordinary.code != 1 || ordinary.stdout != "" || ordinary.stderr == "" || strings.Contains(ordinary.stderr, "synthetic-private-") {
				t.Fatalf("local failure control changed: %+v", ordinary)
			}
			verbose := runMainDispatchWithEnv(t, "bash", env, append(args, "--verbose")...)
			if verbose.code != ordinary.code || verbose.stdout != ordinary.stdout {
				t.Fatalf("verbose changed failure status or data: %+v", verbose)
			}
			label := command[0]
			if label == "tokenizer" {
				label += " count"
			}
			assertLocalUtilityVerbose(t, verbose.stderr, label, "auto", "failed", ordinary.stderr)
			quiet := runMainDispatchWithEnv(t, "bash", env, append(args, "--quiet", "--verbose")...)
			if quiet != ordinary {
				t.Fatalf("quiet discarded or decorated the failure: ordinary=%+v quiet=%+v", ordinary, quiet)
			}
			var extracted mainDispatchResult
			for _, flag := range []string{"", "--verbose"} {
				extractArgs := append([]string{"openai", "--transform-error", "message"}, command...)
				if flag != "" {
					extractArgs = append(extractArgs, flag)
				}
				got := runMainDispatchWithEnv(t, "bash", env, extractArgs...)
				var message string
				if got.code != 1 || got.stdout != "" || json.Unmarshal([]byte(got.stderr), &message) != nil || message != strings.TrimSuffix(ordinary.stderr, "\n") {
					t.Fatalf("extracted local error was decorated or lost: %+v", got)
				}
				if flag == "" {
					extracted = got
				} else if got != extracted {
					t.Fatalf("verbose changed extracted error bytes: ordinary=%+v verbose=%+v", extracted, got)
				}
			}
		})
	}
}

func assertLocalUtilityVerbose(t *testing.T, stderr, command, format, outcome, failure string) {
	t.Helper()
	details, _ := removeVerboseElapsed(t, stderr)
	want := "Command: " + command + "\nFormat option: " + format + "\nCommand result: " + outcome + "\n" + failure
	if details != want {
		t.Fatalf("local verbose report changed: got %q; want %q", details, want)
	}
}

func TestMainDispatchOutputTokenizerPrivateProtocols(t *testing.T) {
	for _, helper := range []string{"__preview", "__output"} {
		t.Run(helper, func(t *testing.T) {
			binaryPath, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			// Use production startup and the actual helper argv. Parent feedback
			// flags are never forwarded to these reserved binary protocols.
			child := exec.CommandContext(ctx, binaryPath, "-test.run=^TestMainDispatchProcess$", "--", "openai", "tokenizer", helper)
			child.Env = append(localUtilitiesEnvironment(t), "OPENAI_CLI_MAIN_DISPATCH_PROCESS=1", "GOMAXPROCS=2",
				"OPENAI_BASE_URL=://synthetic-private-endpoint", "SYSTEMROOT="+os.Getenv("SYSTEMROOT"))
			input, err := child.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			cleanupLocalUtilityPipe(t, input)
			output, err := child.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			cleanupLocalUtilityPipe(t, output)
			diagnostic, err := child.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			cleanupLocalUtilityPipe(t, diagnostic)
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				cancel()
				if !waited {
					_ = child.Wait()
				}
			}()
			if helper == "__preview" {
				frame := append([]byte("OAITOK\x01\x02\x00\x00\x00\x0d"), []byte("Hello, world!")...)
				writeLocalUtilityFrame(t, input, frame)
				want := []byte("OAITOK\x01\x02\x00\x00\x00\x04")
				for _, token := range [][2]uint32{{15496, 5}, {11, 6}, {995, 12}, {0, 13}} {
					want = binary.BigEndian.AppendUint32(want, token[0])
					want = binary.BigEndian.AppendUint32(want, token[1])
				}
				readLocalUtilityFrame(t, output, want)
			} else {
				readLocalUtilityFrame(t, diagnostic, []byte("TOKOUT\x01\x00"))
				payload := []byte("synthetic\x00\xff\x1b[31m\r\nframe")
				frame := binary.BigEndian.AppendUint32(nil, 1)
				frame = binary.BigEndian.AppendUint32(frame, uint32(len(payload)))
				writeLocalUtilityFrame(t, input, append(frame, payload...))
				readLocalUtilityFrame(t, output, payload)
				ack := binary.BigEndian.AppendUint32(nil, 1)
				ack = binary.BigEndian.AppendUint32(ack, uint32(len(payload)))
				readLocalUtilityFrame(t, diagnostic, append(ack, 0))
			}
			if helper == "__output" {
				if err := input.Close(); err != nil {
					t.Fatal(err)
				}
			}
			stdoutTail, outErr := io.ReadAll(output)
			stderrTail, errErr := io.ReadAll(diagnostic)
			if helper == "__preview" {
				// Keep the parent lifeline open until the child finishes. Closing
				// it beside the final response can intentionally cancel the child.
				if err := input.Close(); err != nil {
					t.Fatal(err)
				}
			}
			waitErr := child.Wait()
			waited = true
			if outErr != nil || errErr != nil || waitErr != nil || ctx.Err() != nil || len(stdoutTail) != 0 || len(stderrTail) != 0 {
				t.Fatalf("private protocol exit changed: stdout=%q stderr=%q read=%v/%v wait=%v context=%v", stdoutTail, stderrTail, outErr, errErr, waitErr, ctx.Err())
			}
		})
	}
}

func cleanupLocalUtilityPipe(t *testing.T, pipe io.Closer) {
	t.Helper()
	t.Cleanup(func() {
		if err := pipe.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Errorf("close private protocol pipe: %v", err)
		}
	})
}

func writeLocalUtilityFrame(t *testing.T, out io.Writer, frame []byte) {
	t.Helper()
	if n, err := out.Write(frame); err != nil || n != len(frame) {
		t.Fatalf("private protocol input: wrote %d/%d, error %v", n, len(frame), err)
	}
}

func readLocalUtilityFrame(t *testing.T, input io.Reader, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	if _, err := io.ReadFull(input, got); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("private protocol bytes: got %x want %x, error %v", got, want, err)
	}
}
