package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// These checks cover native noninteractive shells, not terminal applications.
func TestMainNativeShellFiniteJSONList(t *testing.T) {
	work, binary := buildNativeShellCLI(t)
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	shells := []nativeShell{
		{"bash", "bash", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"--noprofile", "--norc", "-c"}},
		{"zsh", "zsh", "", "export OPENAI_API_KEY='fake-native-shell-key'; ", []string{"-f", "-c"}},
		{"fish", "fish", "", "set -gx OPENAI_API_KEY 'fake-native-shell-key'; ", []string{"--no-config", "--private", "-c"}},
	}
	if runtime.GOOS == "windows" {
		shells = []nativeShell{
			{"cmd", "cmd.exe", "", `set "OPENAI_API_KEY=fake-native-shell-key" && `, []string{"/D", "/C"}},
			{"powershell", "powershell.exe", "", `$env:OPENAI_API_KEY = 'fake-native-shell-key'; `, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
			{"pwsh", "pwsh.exe", "", `$env:OPENAI_API_KEY = 'fake-native-shell-key'; `, []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}},
		}
	}
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, name := range required {
		if name != "" && !slices.ContainsFunc(shells, func(shell nativeShell) bool { return shell.name == name }) {
			t.Fatalf("required shell %q is not configured on %s", name, runtime.GOOS)
		}
	}
	items := []string{
		`{"id":"file_a","object":"file","filename":"café Ω 漢字 😺","custom":{"count":9007199254740993,"retained":true}}`,
		`{"id":"file_b","object":"file","filename":"second Ω file","custom":{"count":9007199254740995,"retained":false}}`,
	}
	controlJSON := []byte("[" + strings.Join(items, ",") + "]\n")
	original, err := finiteJSONListShellRecords(controlJSON, "json")
	if err != nil {
		t.Fatal(err)
	}
	const apiMessage = "synthetic later shell page failure"
	for _, shell := range shells {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required native shell unavailable: %v", err)
				}
				t.Skipf("native shell unavailable: %v", err)
			}
			shell.executable = path
			if runtime.GOOS == "windows" {
				// Reuse the existing kill-on-close Job Object shell supervisor.
				shell.executable = helper
				shell.args = append([]string{"-test.run=^TestShellFileWindowsProcessHelper$", "--", path}, shell.args...)
			}
			invoke := func(executable string, args ...string) string {
				values := append([]string{executable}, args...)
				for index := range values {
					values[index] = finiteJSONListShellQuote(shell, values[index])
				}
				prefix := ""
				if shell.name == "powershell" || shell.name == "pwsh" {
					prefix = "& "
				}
				return prefix + strings.Join(values, " ")
			}
			transport := func(mode, destination string) string {
				return invoke(helper, "-test.run=^TestFiniteJSONListShellProcess$", "--", mode, destination)
			}
			// Unchanged native producers distinguish shell text conversion from
			// CLI behavior. Only the Unicode filename may vary in PowerShell.
			controls := make(map[bool][]map[string]any, 2)
			controlEncodings := make(map[bool]string, 2)
			for _, pipeline := range []bool{false, true} {
				directory := t.TempDir()
				fixture := filepath.Join(directory, "native-control.json")
				if err := os.WriteFile(fixture, controlJSON, 0600); err != nil {
					t.Fatal(err)
				}
				output := filepath.Join(directory, "control-output.json")
				consumer := ""
				if pipeline {
					consumer = transport("parse-json", output)
				}
				script, status := finiteJSONListShellScript(t, shell, directory, transport("produce", fixture), consumer, output)
				got := runNativeShell(t, shell, directory, t.TempDir(), "http://127.0.0.1:1", script)
				assertFiniteJSONListShellStatus(t, status, 0, got, fmt.Sprintf("native producer control pipeline=%t", pipeline))
				if got.code != 0 || got.stdout != "" || got.stderr != "" {
					t.Fatalf("native producer control failed: pipeline=%t result=%+v", pipeline, got)
				}
				data, encoding := readFiniteJSONListShellText(t, output)
				if pipeline {
					_, encoding = readFiniteJSONListShellText(t, output+".stdin")
				}
				observed, err := finiteJSONListShellRecords(data, "json")
				if err != nil {
					t.Fatalf("native producer control is not a complete array: %v", err)
				}
				if len(observed) != len(original) {
					t.Fatalf("native producer control changed record count: %d", len(observed))
				}
				for index, item := range observed {
					want := make(map[string]any, len(original[index]))
					for key, value := range original[index] {
						want[key] = value
					}
					if shell.name == "powershell" || shell.name == "pwsh" {
						filename, ok := item["filename"].(string)
						if !ok || filename == "" {
							t.Fatalf("native producer control lost its text field: %#v", item)
						}
						want["filename"] = filename
					}
					if !reflect.DeepEqual(item, want) {
						t.Fatalf("native producer control changed record: got=%#v want=%#v", item, want)
					}
				}
				controls[pipeline] = observed
				controlEncodings[pipeline] = encoding
				t.Logf("native control: pipeline=%t encoding=%s Unicode-preserved=%t", pipeline, encoding, reflect.DeepEqual(observed, original))
			}
			for _, test := range []struct {
				name, format             string
				pipeline, empty, failure bool
			}{
				{"pipeline_json_multipage", "json", true, false, false},
				{"redirect_json_multipage", "json", false, false, false},
				{"redirect_json_empty", "json", false, true, false},
				{"pipeline_jsonl_multipage", "jsonl", true, false, false},
				{"redirect_later_http_failure", "json", false, false, true},
			} {
				t.Run(test.name, func(t *testing.T) {
					var requests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						request := requests.Add(1)
						after := r.URL.Query().Get("after")
						validCursor := request == 1 && after == "" || !test.empty && request == 2 && after == "file_a"
						if r.Method != http.MethodGet || r.URL.Path != "/files" || !validCursor || r.Header.Get("Authorization") != "Bearer fake-native-shell-key" {
							t.Errorf("invalid synthetic list request %d: %s %s", request, r.Method, r.URL)
							http.Error(w, "invalid synthetic request", http.StatusBadRequest)
							return
						}
						w.Header().Set("Content-Type", "application/json")
						if test.empty {
							io.WriteString(w, resourceFilePage(nil, false))
						} else if request == 1 {
							fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":true,"last_id":"file_a"}`, items[0])
						} else if test.failure {
							w.WriteHeader(http.StatusBadRequest)
							fmt.Fprintf(w, `{"error":{"message":%q,"type":"invalid_request_error"}}`, apiMessage)
						} else {
							fmt.Fprintf(w, `{"object":"list","data":[%s],"has_more":false,"last_id":"file_b"}`, items[1])
						}
					}))
					defer server.Close()
					directory := t.TempDir()
					output := filepath.Join(directory, "saved list & apostrophe's.json")
					consumer := ""
					if test.pipeline {
						consumer = transport("parse-"+test.format, output)
					}
					producer := shell.setKey + invoke(filepath.Join(work, binary), "--format", test.format, "files", "list")
					script, status := finiteJSONListShellScript(t, shell, directory, producer, consumer, output)
					got := runNativeShell(t, shell, directory, t.TempDir(), server.URL, script)
					wantCode, wantRequests := 0, int32(2)
					if test.failure {
						wantCode = 1
					}
					if test.empty {
						wantRequests = 1
					}
					assertFiniteJSONListShellStatus(t, status, wantCode, got, "CLI producer")
					if got.code != wantCode || got.stdout != "" || requests.Load() != wantRequests {
						t.Fatalf("shell result changed: result=%+v requests=%d", got, requests.Load())
					}
					data, encoding := readFiniteJSONListShellText(t, output)
					if test.pipeline {
						_, encoding = readFiniteJSONListShellText(t, output+".stdin")
					}
					if encoding != controlEncodings[test.pipeline] {
						t.Fatalf("CLI transport encoding=%s; native control=%s", encoding, controlEncodings[test.pipeline])
					}
					if test.failure {
						assertFiniteJSONListShellPartial(t, data, controls[false][0])
						if shell.name == "powershell" || shell.name == "pwsh" {
							// PowerShell may decorate native stderr. Preserve the whole
							// capture without claiming that stripped prose is JSON.
							if !strings.Contains(got.stderr, apiMessage) || !strings.Contains(got.stderr, "invalid_request_error") {
								t.Fatalf("PowerShell lost API diagnostic details: %q", got.stderr)
							}
						} else {
							var diagnostic map[string]any
							want := map[string]any{"message": apiMessage, "type": "invalid_request_error"}
							if err := json.Unmarshal([]byte(got.stderr), &diagnostic); err != nil || !reflect.DeepEqual(diagnostic, want) {
								t.Fatalf("API diagnostic changed: %q error=%v", got.stderr, err)
							}
						}
						return
					}
					if got.stderr != "" {
						t.Fatalf("successful shell command wrote stderr: %q", got.stderr)
					}
					// Parser receipts always contain an array, including JSONL inputs.
					format := test.format
					if test.pipeline {
						format = "json"
					}
					actual, err := finiteJSONListShellRecords(data, format)
					want := controls[test.pipeline]
					if test.empty {
						want = []map[string]any{}
					}
					if err != nil || !reflect.DeepEqual(actual, want) {
						t.Fatalf("complete shell output changed: got=%#v want=%#v error=%v", actual, want, err)
					}
				})
			}
		})
	}
}

func finiteJSONListShellQuote(shell nativeShell, value string) string {
	if shell.name == "cmd" {
		return `"` + value + `"`
	}
	if shell.name == "powershell" || shell.name == "pwsh" {
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Record the producer's status before another command or pipeline consumer can
// replace it. The shell result independently establishes the consumer status.
func finiteJSONListShellScript(t *testing.T, shell nativeShell, directory, producer, consumer, output string) (string, string) {
	t.Helper()
	status := filepath.Join(directory, "producer-status.txt")
	quote := func(value string) string { return finiteJSONListShellQuote(shell, value) }
	if consumer == "" {
		producer += " > " + quote(output)
	}
	var body, finish string
	switch shell.name {
	case "cmd":
		body = producer + "\r\nset \"producer_exit=%errorlevel%\"\r\n> " + quote(status) + " echo %producer_exit%\r\n"
		finish = "exit /b %producer_exit%"
		if consumer != "" {
			file := filepath.Join(directory, "finite-list-producer.cmd")
			if err := os.WriteFile(file, []byte("@echo off\r\n"+body+finish+"\r\n"), 0600); err != nil {
				t.Fatal(err)
			}
			return "call " + quote(file) + " | " + consumer, status
		}
	case "powershell", "pwsh":
		body = producer + "; $producer_exit = $LASTEXITCODE; [IO.File]::WriteAllText(" + quote(status) + ", [string]$producer_exit)"
		finish = "; exit $producer_exit"
		if consumer != "" {
			return "& { " + body + " } | " + consumer, status
		}
	case "fish":
		body = producer + "; set producer_exit $status; printf '%s\\n' $producer_exit > " + quote(status)
		finish = "; exit $producer_exit"
		if consumer != "" {
			return "begin; " + body + "; end | " + consumer, status
		}
	default:
		body = producer + "; producer_exit=$?; printf '%s\\n' \"$producer_exit\" > " + quote(status)
		finish = "; exit \"$producer_exit\""
		if consumer != "" {
			return "( " + body + " ) | " + consumer, status
		}
	}
	return body + finish, status
}

func assertFiniteJSONListShellStatus(t *testing.T, path string, want int, result mainDispatchResult, phase string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: read producer status: %v; shell code=%d stdout=%q stderr=%q", phase, err, result.code, result.stdout, result.stderr)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || code != want {
		t.Fatalf("%s: producer exit=%q; want %d; error=%v; shell code=%d stdout=%q stderr=%q", phase, data, want, err, result.code, result.stdout, result.stderr)
	}
}

func readFiniteJSONListShellText(t *testing.T, path string) ([]byte, string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	encoding := "UTF-8"
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) {
		encoding, data = "UTF-16LE", data[2:]
		if len(data)%2 != 0 {
			t.Fatal("redirected UTF-16LE contains an incomplete code unit")
		}
		units := make([]uint16, len(data)/2)
		for index := range units {
			units[index] = binary.LittleEndian.Uint16(data[2*index:])
		}
		for index := 0; index < len(units); index++ {
			value := units[index]
			if value >= 0xd800 && value <= 0xdbff {
				if index+1 >= len(units) || units[index+1] < 0xdc00 || units[index+1] > 0xdfff {
					t.Fatal("redirected UTF-16LE contains an unpaired high surrogate")
				}
				index++
			} else if value >= 0xdc00 && value <= 0xdfff {
				t.Fatal("redirected UTF-16LE contains an unpaired low surrogate")
			}
		}
		data = []byte(string(utf16.Decode(units)))
	} else if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		encoding, data = "UTF-8-BOM", data[3:]
	}
	if !utf8.Valid(data) {
		t.Fatal("shell output is not valid text in its observed encoding")
	}
	return data, encoding
}

func finiteJSONListShellRecords(data []byte, format string) ([]map[string]any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("JSON input is not valid UTF-8")
	}
	decode := func(data []byte, destination any) error {
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err := decoder.Decode(destination); err != nil {
			return err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return fmt.Errorf("expected EOF after the JSON value, got %v", err)
		}
		return nil
	}
	var records []map[string]any
	if format == "json" {
		if err := decode(data, &records); err != nil {
			return nil, err
		}
		if records == nil {
			return nil, fmt.Errorf("expected an array, got null")
		}
	} else {
		for _, line := range bytes.Split(bytes.TrimSpace(data), []byte{'\n'}) {
			var record map[string]any
			if err := decode(line, &record); err != nil {
				return nil, err
			}
			records = append(records, record)
		}
	}
	for _, record := range records {
		if record == nil {
			return nil, fmt.Errorf("expected an object, got null")
		}
	}
	return records, nil
}

func assertFiniteJSONListShellPartial(t *testing.T, data []byte, want map[string]any) {
	t.Helper()
	reader := bytes.NewReader(data)
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		t.Fatalf("missing partial array prefix: token=%v error=%v", token, err)
	}
	var first map[string]any
	if err := decoder.Decode(&first); err != nil || !reflect.DeepEqual(first, want) {
		t.Fatalf("partial array lost its first item: got=%#v want=%#v error=%v", first, want, err)
	}
	tail, err := io.ReadAll(io.MultiReader(decoder.Buffered(), reader))
	if err != nil || len(bytes.TrimSpace(tail)) != 0 {
		t.Fatalf("failure completed or corrupted its partial array: suffix=%q error=%v", tail, err)
	}
}

// The native producer exposes shell conversions without CLI formatting code.
// The parser consumes the whole pipe and exits without Go test harness output.
func TestFiniteJSONListShellProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) != 2 {
		t.Fatal("invalid native list helper arguments")
	}
	watchdog := time.AfterFunc(15*time.Second, func() { os.Exit(124) })
	defer watchdog.Stop()
	var err error
	switch args[0] {
	case "produce":
		var data []byte
		data, err = os.ReadFile(args[1])
		if err == nil {
			_, err = os.Stdout.Write(data)
		}
	case "parse-json", "parse-jsonl":
		var data []byte
		data, err = io.ReadAll(os.Stdin)
		if err == nil {
			// Keep native-shell bytes before decoding its observed text encoding.
			// A shell may add a BOM even when its native producer writes none.
			err = os.WriteFile(args[1]+".stdin", data, 0600)
		}
		if err == nil {
			var encoding string
			data, encoding = readFiniteJSONListShellText(t, args[1]+".stdin")
			var records []map[string]any
			records, err = finiteJSONListShellRecords(data, strings.TrimPrefix(args[0], "parse-"))
			if err != nil {
				err = fmt.Errorf("native pipeline encoding=%s decoded prefix=% x: %w", encoding, data[:min(8, len(data))], err)
			}
			if err == nil {
				data, err = json.Marshal(records)
			}
			if err == nil {
				err = os.WriteFile(args[1], append(data, '\n'), 0600)
			}
		}
	default:
		t.Fatal("unknown native list helper mode")
	}
	if err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
