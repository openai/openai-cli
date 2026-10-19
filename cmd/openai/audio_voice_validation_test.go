package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

const audioVoiceInvalidType = "Invalid voice type: --type must be audio_sample or prompt. Omit it to use audio_sample."

func audioVoiceMissing(route []string, fields string) string {
	return "Missing required options: " + fields + ".\nOptions and examples: openai " + strings.Join(route, " ") + " --help"
}

func audioVoiceJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

// Capture request observations without assertions in the HTTP handler goroutine.
// The test checks this completed record after the child and server have stopped.
type audioVoiceCapturedRequest struct {
	method, path            string
	values                  map[string][]string
	files                   map[string][][]byte
	filenames, contentTypes map[string][]string
	err                     error
}

func captureAudioVoiceRequest(r *http.Request) audioVoiceCapturedRequest {
	got := audioVoiceCapturedRequest{
		method: r.Method, path: r.URL.Path,
		values: map[string][]string{}, files: map[string][][]byte{},
		filenames: map[string][]string{}, contentTypes: map[string][]string{},
	}
	reader, err := r.MultipartReader()
	if err != nil {
		got.err = err
		return got
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			return got
		}
		if err != nil {
			got.err = err
			return got
		}
		data, readErr := io.ReadAll(part)
		if err := errors.Join(readErr, part.Close()); err != nil {
			got.err = err
			return got
		}
		name := part.FormName()
		if filename := part.FileName(); filename != "" {
			got.files[name] = append(got.files[name], data)
			got.filenames[name] = append(got.filenames[name], filename)
			got.contentTypes[name] = append(got.contentTypes[name], part.Header.Get("Content-Type"))
		} else {
			got.values[name] = append(got.values[name], string(data))
		}
	}
}

func TestMainAudioVoicePreflight(t *testing.T) {
	for _, route := range [][]string{{"audio", "voices", "create"}, {"audio:voices", "create"}} {
		t.Run(strings.Join(route, " "), func(t *testing.T) {
			home, env, requests := localRecoveryEnvironment(t)
			missing := filepath.Join(home, "synthetic-private-missing.wav")
			typeReference := "@" + filepath.Join(home, "synthetic-private-type.txt")
			type failureCase struct {
				name, input, missing, message string
				flags, env                    []string
				structured, help              bool
			}
			cases := []failureCase{
				{name: "all required", missing: "--audio-sample, --consent, --name"},
				{name: "default consent before file", flags: []string{"--name", "Synthetic", "--audio-sample", missing}, missing: "--consent", help: true},
				{name: "explicit consent before file", flags: []string{"--type", "audio_sample", "--name", "Synthetic", "--audio-sample", missing}, missing: "--consent"},
				{name: "consent and name order", flags: []string{"--audio-sample", missing}, missing: "--consent, --name"},
				{name: "sample and consent order", flags: []string{"--name", "Synthetic"}, missing: "--audio-sample, --consent"},
				{name: "name before file", flags: []string{"--consent", "cons_synthetic", "--audio-sample", missing}, missing: "--name"},
				{name: "prompt required order", flags: []string{"--type", "prompt", "--audio-sample", missing}, missing: "--name, --prompt"},
				{name: "prompt missing", flags: []string{"--type", "prompt", "--name", "Synthetic", "--audio-sample", missing}, missing: "--prompt"},
				{name: "prompt name missing", flags: []string{"--type", "prompt", "--prompt", "Synthetic", "--audio-sample", missing}, missing: "--name"},
				{name: "complete requirements retain file error", flags: []string{"--name", "Synthetic", "--consent", "cons_synthetic", "--audio-sample", missing}, message: "Could not open the file for --audio-sample. Check the path and permissions."},
				{name: "unresolved file requires name", flags: []string{"--type", typeReference, "--audio-sample", missing}, missing: "--name"},
				{name: "unresolved file retains file error", flags: []string{"--name", "Synthetic", "--type", typeReference}, message: "Could not open the file for --type. Check the path and permissions."},
				{name: "protected reference stays literal", input: audioVoiceJSON(t, map[string]any{"type": typeReference}), env: []string{"OPENAI_UNTRUSTED_STDIN=true"}, flags: []string{"--audio-sample", missing}, message: audioVoiceInvalidType},
				{name: "protected stdin reference stays literal", input: `{"type":"@-"}`, env: []string{"OPENAI_UNTRUSTED_STDIN=true"}, flags: []string{"--audio-sample", missing}, message: audioVoiceInvalidType},
				{name: "protected upload remains rejected", input: audioVoiceJSON(t, map[string]any{"type": "audio_sample", "name": "Synthetic", "consent": "cons_synthetic", "audio_sample": missing}), env: []string{"OPENAI_UNTRUSTED_STDIN=true"}, message: "File input from piped YAML/JSON is disabled when OPENAI_UNTRUSTED_STDIN is enabled; provide --audio-sample explicitly."},
				{name: "JSON required guidance", flags: []string{"--name", "Synthetic", "--audio-sample", missing}, missing: "--consent", structured: true, help: true},
				{name: "JSON invalid type", flags: []string{"--type", "synthetic-private-invalid\x1b[31m", "--audio-sample", missing}, message: audioVoiceInvalidType, structured: true},
				{name: "JSON file guidance", flags: []string{"--name", "Synthetic", "--consent", "cons_synthetic", "--audio-sample", missing}, message: "Could not open the file for --audio-sample. Check the path and permissions.", structured: true},
			}
			for _, literal := range []struct{ name, value string }{
				{"unknown", "synthetic-private-invalid"}, {"empty", ""}, {"whitespace", " prompt "},
				{"case variant", "PROMPT"}, {"trailing newline", "prompt\n"}, {"quoted", `"prompt"`}, {"escaped reference", `\@synthetic-private`},
			} {
				cases = append(cases, failureCase{name: "literal " + literal.name, flags: []string{"--type", literal.value, "--audio-sample", missing}, message: audioVoiceInvalidType})
			}
			for _, scalar := range []struct {
				name  string
				value any
			}{
				{"null", nil}, {"boolean", false}, {"number", 0},
				{"array reference", []any{typeReference}}, {"mapping reference", map[string]any{"kind": typeReference}},
			} {
				body := audioVoiceJSON(t, map[string]any{"type": scalar.value})
				cases = append(cases, failureCase{name: "nonstring " + scalar.name, input: body, flags: []string{"--audio-sample", missing}, message: audioVoiceInvalidType})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					mode := localRecoveryMode{name: "text"}
					if tc.structured {
						mode.name, mode.structured = "json", true
					}
					args := append([]string{"openai", "--format-error", mode.name}, route...)
					args = append(args, tc.flags...)
					input := shellFileInput(t, []byte(tc.input))
					got := runMainDispatchWithStdin(t, "", append(append([]string{}, env...), tc.env...), input, args...)
					message := localRecoveryMessage(t, got, mode, 1)
					want := tc.message
					if tc.missing != "" {
						want = audioVoiceMissing(route, tc.missing)
					}
					require.Equal(t, want, message)
					require.Zero(t, requests.Load())
					if tc.help {
						_, copied, found := strings.Cut(message, "\nOptions and examples: ")
						require.True(t, found)
						require.Equal(t, "openai "+strings.Join(route, " ")+" --help", copied)
						// This exact command contains no quoting. Spaced paths have separate shell coverage.
						help := runMainDispatchWithEnv(t, "", env, strings.Fields(copied)...)
						require.Zero(t, help.code, help.stderr)
						require.Empty(t, help.stderr)
						require.True(t, strings.HasPrefix(help.stdout, "NAME:\n   openai "+strings.Join(route, " ")+"\n"))
						for _, text := range []string{"\nSYNOPSIS:\n", "\nOPTIONS:", "--name", "--type", "--consent", "--audio-sample"} {
							require.Contains(t, help.stdout, text)
						}
						require.Zero(t, requests.Load())
					}
				})
			}
		})
	}
}

func TestMainAudioVoiceMergedInputs(t *testing.T) {
	for _, route := range [][]string{{"audio", "voices", "create"}, {"audio:voices", "create"}} {
		t.Run(strings.Join(route, " "), func(t *testing.T) {
			sample := filepath.Join(t.TempDir(), "sample.wav")
			sampleBytes := []byte("synthetic sample bytes\x00\xff")
			require.NoError(t, os.WriteFile(sample, sampleBytes, 0600))
			missing := filepath.Join(t.TempDir(), "synthetic-private-missing.wav")
			for _, tc := range []struct {
				name, input string
				flags, env  []string
				values      map[string][]string
				sample      bool
			}{
				{name: "prompt flags", flags: []string{"--type", "prompt", "--name", "Synthetic", "--prompt", "Synthetic narrator"}, values: map[string][]string{"type": {"prompt"}, "name": {"Synthetic"}, "prompt": {"Synthetic narrator"}}},
				{name: "ready JSON", input: `{"type":"prompt","name":"Synthetic","prompt":"Synthetic narrator","extra":"retained"}`, values: map[string][]string{"type": {"prompt"}, "name": {"Synthetic"}, "prompt": {"Synthetic narrator"}, "extra": {"retained"}}},
				{name: "ready YAML", input: "name: Synthetic\ntype: audio_sample\nconsent: cons_synthetic\nextra: retained\n", flags: []string{"--audio-sample", sample}, values: map[string][]string{"type": {"audio_sample"}, "name": {"Synthetic"}, "consent": {"cons_synthetic"}, "extra": {"retained"}}, sample: true},
				{name: "explicit prompt overrides sample", input: `{"type":"audio_sample","name":"Piped","extra":"retained"}`, flags: []string{"--type", "prompt", "--name", "Synthetic", "--prompt", "Synthetic narrator"}, values: map[string][]string{"type": {"prompt"}, "name": {"Synthetic"}, "prompt": {"Synthetic narrator"}, "extra": {"retained"}}},
				{name: "explicit sample overrides prompt", input: `{"type":"prompt","name":"Piped","extra":"retained"}`, flags: []string{"--type", "audio_sample", "--name", "Synthetic", "--consent", "cons_synthetic", "--audio-sample", sample}, values: map[string][]string{"type": {"audio_sample"}, "name": {"Synthetic"}, "consent": {"cons_synthetic"}, "extra": {"retained"}}, sample: true},
				{name: "explicit type overrides invalid aggregate", input: `{"type":["synthetic-private-invalid"],"name":"Synthetic","prompt":"Synthetic narrator"}`, flags: []string{"--type", "prompt"}, values: map[string][]string{"type": {"prompt"}, "name": {"Synthetic"}, "prompt": {"Synthetic narrator"}}},
				{name: "protected prompt stays literal", input: `{"type":"prompt","name":"Synthetic","prompt":"@synthetic-private-missing.txt"}`, env: []string{"OPENAI_UNTRUSTED_STDIN=true"}, values: map[string][]string{"type": {"prompt"}, "name": {"Synthetic"}, "prompt": {"@synthetic-private-missing.txt"}}},
				{name: "protected sample with explicit upload override", input: audioVoiceJSON(t, map[string]any{"type": "audio_sample", "name": "Synthetic", "consent": "cons_synthetic", "audio_sample": missing}), flags: []string{"--audio-sample", sample}, env: []string{"OPENAI_UNTRUSTED_STDIN=true"}, values: map[string][]string{"type": {"audio_sample"}, "name": {"Synthetic"}, "consent": {"cons_synthetic"}}, sample: true},
				{name: "empty sample requirements remain present", input: `{"name":"","consent":""}`, flags: []string{"--audio-sample", sample}, values: map[string][]string{"name": {""}, "consent": {""}}, sample: true},
				{name: "null sample requirements remain present", input: `{"name":null,"consent":null}`, flags: []string{"--audio-sample", sample}, values: map[string][]string{"name": {""}, "consent": {""}}, sample: true},
				{name: "empty prompt requirements remain present", input: `{"type":"prompt","name":"","prompt":""}`, values: map[string][]string{"type": {"prompt"}, "name": {""}, "prompt": {""}}},
				{name: "null prompt requirements remain present", input: `{"type":"prompt","name":null,"prompt":null}`, values: map[string][]string{"type": {"prompt"}, "name": {""}, "prompt": {""}}},
				{name: "false and zero remain present", input: `{"type":"prompt","name":false,"prompt":0}`, values: map[string][]string{"type": {"prompt"}, "name": {"false"}, "prompt": {"0"}}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var count atomic.Int32
					requests := make(chan audioVoiceCapturedRequest, 1)
					const response = `{"id":"voice_synthetic","name":"Synthetic"}`
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						count.Add(1)
						captured := captureAudioVoiceRequest(r)
						w.Header().Set("Content-Type", "application/json")
						_, err := io.WriteString(w, response)
						captured.err = errors.Join(captured.err, err)
						select {
						case requests <- captured:
						default:
							// The final count rejects duplicates without blocking the handler.
						}
					}))
					defer server.Close()
					got := runShellFileCommand(t, server, shellFileInput(t, []byte(tc.input)), tc.env, append(append([]string{}, route...), tc.flags...)...)
					server.Close()
					require.Zero(t, got.code, got.stderr)
					require.Empty(t, got.stderr)
					require.JSONEq(t, response, got.stdout)
					require.EqualValues(t, 1, count.Load())
					require.Len(t, requests, 1)
					captured := <-requests
					require.NoError(t, captured.err)
					require.Equal(t, http.MethodPost, captured.method)
					require.Equal(t, "/audio/voices", captured.path)
					require.Equal(t, tc.values, captured.values)
					wantFiles := map[string][][]byte{}
					if tc.sample {
						wantFiles["audio_sample"] = [][]byte{sampleBytes}
					}
					require.Equal(t, wantFiles, captured.files)
				})
			}
		})
	}
}

func TestMainAudioVoiceDiscriminatorReferences(t *testing.T) {
	for _, route := range [][]string{{"audio", "voices", "create"}, {"audio:voices", "create"}} {
		for _, voiceType := range []string{"audio_sample", "prompt"} {
			for _, source := range []string{"file", "stdin"} {
				t.Run(strings.Join(route, " ")+"/"+voiceType+"/"+source, func(t *testing.T) {
					var count atomic.Int32
					const response = `{"id":"voice_synthetic"}`
					requests := make(chan audioVoiceCapturedRequest, 1)
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						count.Add(1)
						captured := captureAudioVoiceRequest(r)
						w.Header().Set("Content-Type", "application/json")
						_, err := io.WriteString(w, response)
						captured.err = errors.Join(captured.err, err)
						select {
						case requests <- captured:
						default:
							// The final count rejects duplicates without blocking the handler.
						}
					}))
					defer server.Close()
					input, reference := "", "@-"
					if source == "file" {
						path := filepath.Join(t.TempDir(), "voice-type.txt")
						require.NoError(t, os.WriteFile(path, []byte(voiceType), 0600))
						reference = "@" + path
					} else {
						input = voiceType
					}
					args := append(append([]string{}, route...), "--name", "Synthetic", "--type", reference)
					if voiceType == "prompt" {
						args = append(args, "--prompt", "Synthetic narrator")
					} else {
						sample := filepath.Join(t.TempDir(), "sample.wav")
						require.NoError(t, os.WriteFile(sample, []byte("synthetic sample"), 0600))
						args = append(args, "--consent", "cons_synthetic", "--audio-sample", sample)
					}
					got := runShellFileCommand(t, server, shellFileInput(t, []byte(input)), nil, args...)
					server.Close()
					require.Zero(t, got.code, got.stderr)
					require.Empty(t, got.stderr)
					require.JSONEq(t, response, got.stdout)
					require.EqualValues(t, 1, count.Load())
					require.Len(t, requests, 1)
					captured := <-requests
					require.NoError(t, captured.err)
					require.Equal(t, http.MethodPost, captured.method)
					require.Equal(t, "/audio/voices", captured.path)
					wantValues := map[string][]string{"name": {"Synthetic"}}
					wantFiles := map[string][][]byte{"type": {[]byte(voiceType)}}
					if voiceType == "prompt" {
						wantValues["prompt"] = []string{"Synthetic narrator"}
					} else {
						wantValues["consent"] = []string{"cons_synthetic"}
						wantFiles["audio_sample"] = [][]byte{[]byte("synthetic sample")}
					}
					require.Equal(t, wantValues, captured.values)
					require.Equal(t, wantFiles, captured.files)
					if source == "file" {
						require.Equal(t, []string{"voice-type.txt"}, captured.filenames["type"])
						require.Equal(t, []string{"text/plain; charset=utf-8"}, captured.contentTypes["type"])
					}
				})
			}
		}
		t.Run(strings.Join(route, " ")+"/missing name before open stdin", func(t *testing.T) {
			_, env, requests := localRecoveryEnvironment(t)
			reader, writer, err := os.Pipe()
			require.NoError(t, err)
			t.Cleanup(func() { _ = reader.Close() })
			t.Cleanup(func() { _ = writer.Close() })
			args := append([]string{"openai", "--format-error", "text"}, route...)
			args = append(args, "--type", "@-")
			got := runMainDispatchWithStdin(t, "", env, reader, args...)
			require.Equal(t, audioVoiceMissing(route, "--name"), localRecoveryMessage(t, got, localRecoveryMode{}, 1))
			require.Zero(t, requests.Load())
		})
	}
}

func TestMainAudioVoiceReferenceOrdering(t *testing.T) {
	for _, route := range [][]string{{"audio", "voices", "create"}, {"audio:voices", "create"}} {
		t.Run(strings.Join(route, " "), func(t *testing.T) {
			home, env, requests := localRecoveryEnvironment(t)
			missing := filepath.Join(home, "synthetic-private-missing.wav")
			for _, tc := range []struct {
				name, source, contents, fields, message string
				flags                                   []string
				omitSample, sampleStdin, structured     bool
			}{
				{name: "file missing consent", source: "file", contents: "audio_sample", fields: "--consent"},
				{name: "stdin missing consent", source: "stdin", contents: "audio_sample", fields: "--consent"},
				{name: "file missing prompt", source: "file", contents: "prompt", fields: "--prompt"},
				{name: "stdin missing prompt", source: "stdin", contents: "prompt", fields: "--prompt"},
				{name: "file invalid type", source: "file", contents: "synthetic-private-invalid\x1b[31m", message: audioVoiceInvalidType},
				{name: "stdin invalid type", source: "stdin", contents: "synthetic-private-invalid\x1b[31m", message: audioVoiceInvalidType},
				{name: "file empty type", source: "file", message: audioVoiceInvalidType},
				{name: "stdin empty type", source: "stdin", message: audioVoiceInvalidType},
				{name: "file required field order", source: "file", contents: "audio_sample", omitSample: true, fields: "--audio-sample, --consent"},
				{name: "missing type file before sample", source: "missing", message: "Could not open the file for --type. Check the path and permissions."},
				{name: "JSON missing consent", source: "file", contents: "audio_sample", fields: "--consent", structured: true},
				{name: "JSON invalid referenced type", source: "stdin", contents: "synthetic-private-invalid\x1b[31m", message: audioVoiceInvalidType, structured: true},
				{name: "valid reference retains sample file error", source: "file", contents: "audio_sample", flags: []string{"--consent", "cons_synthetic"}, message: "Could not open the file for --audio-sample. Check the path and permissions."},
				{name: "multiple stdin consumers", source: "stdin", contents: "audio_sample", sampleStdin: true, flags: []string{"--consent", "cons_synthetic"}, message: "multiple request parameters use stdin; select only one stdin consumer"},
				{name: "consumed body rejects type stdin", source: "body stdin", message: "cannot read from stdin: stdin was already consumed by piped YAML/JSON input"},
				{name: "merged file reference precedes sample", source: "body file", contents: "audio_sample", fields: "--consent"},
			} {
				t.Run(tc.name, func(t *testing.T) {
					path := filepath.Join(t.TempDir(), "synthetic-private-type.txt")
					input, reference := "", "@"+path
					switch tc.source {
					case "file", "body file":
						require.NoError(t, os.WriteFile(path, []byte(tc.contents), 0600))
						if tc.source == "body file" {
							input = audioVoiceJSON(t, map[string]any{"type": reference, "name": "Synthetic"})
							reference = ""
						}
					case "stdin":
						input, reference = tc.contents, "@-"
					case "body stdin":
						input, reference = `{"type":"@-","name":"Synthetic","consent":"cons_synthetic"}`, ""
					case "missing":
					default:
						t.Fatal("unknown reference fixture")
					}
					mode := localRecoveryMode{name: "text"}
					if tc.structured {
						mode.name, mode.structured = "json", true
					}
					args := append([]string{"openai", "--format-error", mode.name}, route...)
					args = append(args, "--name", "Synthetic")
					if reference != "" {
						args = append(args, "--type", reference)
					}
					if !tc.omitSample {
						sample := missing
						if tc.sampleStdin {
							sample = "-"
						}
						args = append(args, "--audio-sample", sample)
					}
					args = append(args, tc.flags...)
					got := runMainDispatchWithStdin(t, "", env, shellFileInput(t, []byte(input)), args...)
					want := tc.message
					if tc.fields != "" {
						want = audioVoiceMissing(route, tc.fields)
					}
					require.Equal(t, want, localRecoveryMessage(t, got, mode, 1))
					require.Zero(t, requests.Load())
				})
			}
		})
	}
}
