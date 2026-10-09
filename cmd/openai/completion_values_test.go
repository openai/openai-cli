package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainCompletionValuesProtocols(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			name   string
			args   []string
			values []string
		}{
			{"all formats", []string{"--format", ""}, []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}},
			{"format prefix", []string{"--format", "j"}, []string{"json", "jsonl"}},
			{"error format prefix", []string{"--format-error", "j"}, []string{"json", "jsonl"}},
			{"assigned format", []string{"--format=j"}, []string{"json", "jsonl"}},
			{"empty assigned format", []string{"--format="}, []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}},
			{"assigned error format", []string{"--format-error=r"}, []string{"raw"}},
			{"nested root format", []string{"responses", "create", "--format", "j"}, []string{"json", "jsonl"}},
			{"group root format", []string{"responses", "--format", "j"}, []string{"json", "jsonl"}},
			{"earlier root flags", []string{"--format", "json", "--organization=org-synthetic", "files", "upload", "--purpose", "b"}, []string{"batch"}},
			{"interspersed root flag", []string{"files", "--format", "json", "upload", "--purpose", "v"}, []string{"vision"}},
			{"command alias", []string{"audio:transcriptions", "create", "--format", "j"}, []string{"json", "jsonl"}},
			{"shortcut", []string{"transcribe", "--format-error", "t"}, []string{"text"}},
			{"codex formats", []string{"codex", "--format", ""}, []string{"auto", "text", "json"}},
			{"tokenizer editor formats", []string{"tokenizer", "--format", ""}, []string{"auto", "text"}},
			{"tokenizer count formats", []string{"tokenizer", "count", "--format", ""}, []string{"auto", "text", "json"}},
			{"tokenizer inspect formats", []string{"tokenizer", "inspect", "--format", ""}, []string{"auto", "text", "json"}},
			{"tokenizer encodings formats", []string{"tokenizer", "encodings", "--format", ""}, []string{"auto", "text", "json"}},
			{"tokenizer licenses formats", []string{"tokenizer", "licenses", "--format", ""}, []string{"auto", "text", "json"}},
			{"image preview formats", []string{"images", "preview", "--format", ""}, []string{"auto", "text"}},
			{"image inline on formats", []string{"images", "inline", "on", "--format", ""}, []string{"auto", "text"}},
			{"image inline off formats", []string{"images", "inline", "off", "--format", ""}, []string{"auto", "text"}},
			{"image preview error formats", []string{"images", "preview", "--format-error=j"}, []string{"json", "jsonl"}},
			{"codex assigned format", []string{"codex", "--format=j"}, []string{"json"}},
			{"tokenizer assigned format", []string{"tokenizer", "count", "--format=j"}, []string{"json"}},
			{"codex error formats", []string{"codex", "--format-error", ""}, []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}},
			{"tokenizer editor error formats", []string{"tokenizer", "--format-error", ""}, []string{"auto", "text", "explore", "json", "jsonl", "pretty", "raw", "yaml"}},
			{"tokenizer child error format", []string{"tokenizer", "count", "--format-error=j"}, []string{"json", "jsonl"}},
			{"upload purposes", []string{"files", "upload", "--purpose", ""}, []string{"assistants", "batch", "evals", "fine-tune", "user_data", "vision"}},
			{"legacy create purposes", []string{"files", "create", "--purpose", ""}, []string{"assistants", "batch", "evals", "fine-tune", "user_data", "vision"}},
			{"assigned purpose", []string{"files", "upload", "--purpose=u"}, []string{"user_data"}},
			{"empty assigned purpose", []string{"files", "upload", "--purpose="}, []string{"assistants", "batch", "evals", "fine-tune", "user_data", "vision"}},
			{"list purposes", []string{"files", "list", "--purpose", ""}, []string{"assistants", "assistants_output", "batch", "batch_output", "evals", "fine-tune", "fine-tune-results", "user_data", "vision"}},
			{"list output purpose", []string{"files", "list", "--purpose=batch_"}, []string{"batch_output"}},
			// This case models a shell-decoded preceding filename.
			// Current-token quotes have separate coverage below.
			{"quoted filename precedes value", []string{"files", "upload", "upload space.txt", "--purpose", "u"}, []string{"user_data"}},
		} {
			t.Run(style+"/"+tc.name, func(t *testing.T) {
				prefix := ""
				if name, _, assigned := strings.Cut(tc.args[len(tc.args)-1], "="); assigned && style != "bash" {
					prefix = name + "="
				}
				want := ""
				for _, value := range tc.values {
					want += prefix + value + "\n"
				}
				got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
				if got != (mainDispatchResult{stdout: want}) {
					t.Fatalf("completion got %+v; want stdout %q and status 0", got, want)
				}
			})
		}
	}
}

func TestMainCompletionValuesPreserveOtherInputs(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			args []string
			want mainDispatchResult
		}{
			{[]string{"--format", "unknown"}, mainDispatchResult{code: 11}},
			{[]string{"--format=unknown"}, mainDispatchResult{code: 11}},
			{[]string{"codex", "--format=jsonl"}, mainDispatchResult{code: 11}},
			{[]string{"tokenizer", "--format", "j"}, mainDispatchResult{code: 11}},
			{[]string{"tokenizer", "inspect", "--format", "y"}, mainDispatchResult{code: 11}},
			{[]string{"images", "preview", "--format=j"}, mainDispatchResult{code: 11}},
			{[]string{"images", "inline", "on", "--format", "j"}, mainDispatchResult{code: 11}},
			{[]string{"images", "inline", "off", "--format", "y"}, mainDispatchResult{code: 11}},
			{[]string{"files", "upload", "--purpose", "batch_output"}, mainDispatchResult{code: 11}},
			{[]string{"files", "create", "--purpose=fine-tune-results"}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", "--model", "j"}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", "--model=j"}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", "--", "--format=j"}, mainDispatchResult{}},
			{[]string{"--", "--format=j"}, mainDispatchResult{}},
			{[]string{"files", "upload", "--purpose", "batch", "--file", "j"}, mainDispatchResult{code: 10}},
			{[]string{"files", "upload", "--purpose", "batch", "--file=j"}, mainDispatchResult{code: 10, stdout: "--file=\n"}},
			{[]string{"files", "upload", "--purpose", "batch", "j"}, mainDispatchResult{code: 10}},
			{[]string{"files", "upload", "--purpose", "batch", "--", "--purpose=j"}, mainDispatchResult{code: 10}},
		} {
			t.Run(style+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
				got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1", "OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
				if got != tc.want {
					t.Fatalf("completion got %+v; want %+v", got, tc.want)
				}
			})
		}
	}
}

func TestMainCompletionValuesBashAdapterCompatibility(t *testing.T) {
	for _, marker := range []struct {
		name string
		env  []string
	}{
		{"missing", nil},
		{"empty", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES="}},
		{"zero", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=0"}},
		{"true", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=true"}},
		{"two", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=2"}},
	} {
		for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
			for _, tc := range []struct {
				args []string
				want string
			}{
				{[]string{"--format", "j"}, "json\njsonl\n"},
				{[]string{"--format=j"}, "--format=json\n--format=jsonl\n"},
				{[]string{"--format-error=y"}, "--format-error=yaml\n"},
				{[]string{"files", "upload", "--purpose=u"}, "--purpose=user_data\n"},
			} {
				t.Run(marker.name+"/"+style+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
					want := mainDispatchResult{stdout: tc.want}
					if style == "bash" && strings.Contains(tc.args[len(tc.args)-1], "=") {
						want = mainDispatchResult{code: 11}
					}
					got := runMainDispatchWithEnv(t, style, marker.env, mainCompletionArgs(style, tc.args...)...)
					if got != want {
						t.Fatalf("adapter marker changed completion: got %+v; want %+v", got, want)
					}
				})
			}
		}
	}
}

func TestMainCompletionValuesQuotedArguments(t *testing.T) {
	for _, style := range []string{"zsh", "fish"} {
		for _, quote := range []string{"'", `"`} {
			for _, closing := range []string{"", quote} {
				for _, tc := range []struct {
					path        []string
					flag, value string
					completion  string
				}{
					{nil, "--format", "j", "json\njsonl\n"},
					{nil, "--format", "", "auto\ntext\nexplore\njson\njsonl\npretty\nraw\nyaml\n"},
					{[]string{"files", "upload"}, "--purpose", "u", "user_data\n"},
					{[]string{"files", "upload"}, "--purpose", "", "assistants\nbatch\nevals\nfine-tune\nuser_data\nvision\n"},
				} {
					for _, form := range []string{"separated", "assigned", "whole assignment"} {
						args := append([]string(nil), tc.path...)
						want := tc.completion
						switch form {
						case "separated":
							args = append(args, tc.flag, quote+tc.value+closing)
						case "assigned":
							args = append(args, tc.flag+"="+quote+tc.value+closing)
						case "whole assignment":
							args = append(args, quote+tc.flag+"="+tc.value+closing)
						}
						if form != "separated" {
							want = tc.flag + "=" + strings.ReplaceAll(strings.TrimSuffix(want, "\n"), "\n", "\n"+tc.flag+"=") + "\n"
						}
						t.Run(style+"/"+form+"/"+strings.Join(args, " "), func(t *testing.T) {
							got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, args...)...)
							if got != (mainDispatchResult{stdout: want}) {
								t.Fatalf("quoted completion got %+v; want stdout %q and status 0", got, want)
							}
						})
					}
				}
			}
		}
	}
}

func TestMainCompletionValuesQuotesPreserveOtherInputs(t *testing.T) {
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			args []string
			want mainDispatchResult
		}{
			{[]string{"responses", "create", "--model", `'j'`}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", `--model="j"`}, mainDispatchResult{code: 11}},
			{[]string{"responses", "create", `'--model=j'`}, mainDispatchResult{}},
			{[]string{"files", "upload", "--file", `"fixture"`}, mainDispatchResult{code: 10}},
			{[]string{"files", "upload", `--file='fixture'`}, mainDispatchResult{code: 10, stdout: "--file=\n"}},
			{[]string{`'--mtls-client-cert-file=fixture'`}, mainDispatchResult{}},
			{[]string{"responses", "create", "--", `'--format=j'`}, mainDispatchResult{}},
		} {
			t.Run(style+"/"+strings.Join(tc.args, " "), func(t *testing.T) {
				got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_FILE_VALUES=1", "OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
				if got != tc.want {
					t.Fatalf("quoted control got %+v; want %+v", got, tc.want)
				}
			})
		}
	}
	// Bash and PowerShell dispatch decoded values. Remaining quotes are literal data.
	for _, style := range []string{"bash", "pwsh"} {
		for _, tc := range []struct {
			args []string
			code int
		}{
			{[]string{"--format", `'j`}, 11},
			{[]string{"--format", `"j"`}, 11},
			{[]string{`--format='j'`}, 11},
			{[]string{`'--format=j'`}, 0},
		} {
			t.Run(style+" literal/"+strings.Join(tc.args, " "), func(t *testing.T) {
				got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, tc.args...)...)
				if got != (mainDispatchResult{code: tc.code}) {
					t.Fatalf("completion interpreted literal quotes as shell syntax: %+v", got)
				}
			})
		}
	}
}

func TestMainCompletionValuesPreserveLiteralInnerQuotes(t *testing.T) {
	for _, tc := range []struct {
		path        []string
		flag, value string
	}{
		{nil, "--format", "y"},
		{[]string{"files", "list"}, "--purpose", "u"},
	} {
		for _, quotes := range []struct{ outer, inner string }{{"'", `"`}, {`"`, "'"}} {
			value := quotes.inner + tc.value + quotes.inner
			for _, closing := range []string{"", quotes.outer} {
				for _, form := range []string{"separated", "assigned", "whole assignment"} {
					args := append([]string(nil), tc.path...)
					code := 11
					switch form {
					case "separated":
						args = append(args, tc.flag, quotes.outer+value+closing)
					case "assigned":
						args = append(args, tc.flag+"="+quotes.outer+value+closing)
					case "whole assignment":
						args = append(args, quotes.outer+tc.flag+"="+value+closing)
						code = 0
					}
					t.Run("zsh/"+form+"/"+strings.Join(args, " "), func(t *testing.T) {
						got := runMainDispatchWithEnv(t, "zsh", []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs("zsh", args...)...)
						if got != (mainDispatchResult{code: code}) {
							t.Fatalf("completion removed literal inner quotes: %+v", got)
						}
					})
				}
			}
			// These adapters remove the outer shell quotes before dispatch.
			for _, style := range []string{"bash", "pwsh"} {
				for _, assigned := range []bool{false, true} {
					args := append([]string(nil), tc.path...)
					if assigned {
						args = append(args, tc.flag+"="+value)
					} else {
						args = append(args, tc.flag, value)
					}
					t.Run(style+"/"+strings.Join(args, " "), func(t *testing.T) {
						got := runMainDispatchWithEnv(t, style, []string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, mainCompletionArgs(style, args...)...)
						if got != (mainDispatchResult{code: 11}) {
							t.Fatalf("completion removed literal inner quotes: %+v", got)
						}
					})
				}
			}
		}
	}
}

func TestMainCompletionValuesStayLocal(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		for _, tc := range []struct {
			name string
			env  []string
		}{
			{"configured server", []string{"OPENAI_API_KEY=sk-fake-completion-test", "OPENAI_BASE_URL=" + server.URL}},
			{"invalid configuration", []string{
				"OPENAI_BASE_URL=invalid-completion-url", "OPENAI_CUSTOM_HEADERS=invalid-completion-headers",
				"OPENAI_MTLS_CLIENT_CERT_FILE=/missing/completion-cert.pem", "OPENAI_MTLS_CLIENT_KEY_FILE=/missing/completion-key.pem",
			}},
		} {
			t.Run(style+"/"+tc.name, func(t *testing.T) {
				env := append([]string{"OPENAI_CLI_COMPLETION_STATIC_VALUES=1"}, tc.env...)
				got := runMainDispatchWithEnv(t, style, env, mainCompletionArgs(style, "files", "upload", "--purpose", "u")...)
				if got != (mainDispatchResult{stdout: "user_data\n"}) {
					t.Fatalf("completion depends on request configuration: %+v", got)
				}
			})
		}
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("completion made %d requests", got)
	}
}

func TestMainCompletionValuesDoNotRestrictPurpose(t *testing.T) {
	for _, operation := range []string{"list", "upload", "create"} {
		t.Run(operation, func(t *testing.T) {
			const purpose = "synthetic_future_purpose"
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != "/files" {
					t.Errorf("unexpected request path %q", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if operation == "list" {
					if r.Method != http.MethodGet || r.URL.Query().Get("purpose") != purpose {
						t.Errorf("purpose filter changed: %s %s", r.Method, r.URL)
					}
					_, _ = io.WriteString(w, `{"object":"list","data":[],"has_more":false}`)
					return
				}
				if r.Method != http.MethodPost {
					t.Errorf("unexpected upload method %q", r.Method)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer r.MultipartForm.RemoveAll()
				if got := r.FormValue("purpose"); got != purpose {
					t.Errorf("upload purpose changed: %q", got)
				}
				_, _ = io.WriteString(w, `{"id":"file-synthetic","object":"file","bytes":9,"created_at":1700000000,"filename":"upload.txt","purpose":"synthetic_future_purpose","status":"uploaded"}`)
			}))
			defer server.Close()
			args := []string{"openai", "--format", "json", "files", operation, "--purpose", purpose}
			if operation != "list" {
				path := filepath.Join(t.TempDir(), "upload.txt")
				if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
					t.Fatal(err)
				}
				args = append(args, "--file", path)
			}
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_API_KEY=sk-fake-completion-test", "OPENAI_BASE_URL=" + server.URL}, args...)
			if got.code != 0 || got.stderr != "" || requests.Load() != 1 {
				t.Fatalf("completion metadata restricted a request: %+v; requests=%d", got, requests.Load())
			}
		})
	}
}
