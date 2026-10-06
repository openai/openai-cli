package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openai/openai-cli/pkg/cmd"
)

func TestMainCommandSubgroupsCoverGeneratedRoutes(t *testing.T) {
	count := 0
	for _, old := range cmd.Command.Commands {
		if old.Category != "API RESOURCE" || !strings.Contains(old.Name, ":") {
			continue
		}
		count++
		nested := cmd.Command
		for _, part := range strings.Split(old.Name, ":") {
			nested = nested.Command(part)
			if nested == nil {
				t.Fatalf("missing nested route for %s", old.Name)
			}
		}
		if !old.Hidden || nested.Hidden && nested.Metadata["command-compatibility-alias"] != true {
			t.Fatalf("nested route must stay available for %s", old.Name)
		}
		for _, action := range old.Commands {
			copy := nested.Command(action.Name)
			if copy == nil || copy == action || !slices.Equal(copy.Flags, action.Flags) || (copy.Action == nil) != (action.Action == nil) {
				t.Fatalf("changed action/flag definitions for %s %s", old.Name, action.Name)
			}
		}
	}
	if count < 50 {
		t.Fatalf("expected the complete generated resource tree, got %d routes", count)
	}
	t.Logf("checked %d original resource routes and their nested actions", count)
}

func TestMainCommandSubgroupsHelpAndCompletion(t *testing.T) {
	for _, tc := range []struct{ path, child string }{
		{"admin", "projects"}, {"admin organization", "audit-logs"},
		{"admin organization projects", "service-accounts"},
		{"admin organization projects service-accounts", "api-keys"},
		{"chat", "completions"}, {"chat completions", "messages"},
		{"audio", "transcribe"}, {"beta threads runs", "steps"},
		{"containers files", "content"}, {"skills versions", "content"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			for _, full := range []bool{false, true} {
				args := []string{"openai"}
				if full {
					args = append(args, "help", "--all")
				}
				args = append(args, strings.Fields(tc.path)...)
				if !full {
					args = append(args, "--help")
				}
				got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=invalid"}, args...)
				if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, tc.child) {
					t.Fatalf("offline subgroup help failed: %+v", got)
				}
			}
			for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
				args := append([]string{"openai", "__complete", "--"}, strings.Fields(tc.path)...)
				got := runMainDispatch(t, style, append(args, "")...)
				if got.stderr != "" || !strings.Contains(got.stdout, tc.child) {
					t.Fatalf("%s completion failed: %+v", style, got)
				}
			}
		})
	}
	for _, resource := range [][]string{{"admin:organization:audit-logs"}, {"admin", "organization", "audit-logs"}} {
		got := runMainDispatch(t, "zsh", append(append([]string{"openai", "__complete", "--"}, resource...), "list", "--eff")...)
		if got.stderr != "" || !strings.Contains(got.stdout, "--effective-at") {
			t.Fatalf("compatibility flag completion failed: %+v", got)
		}
	}
	for _, args := range [][]string{
		{"help", "--all", "audio:transcriptions", "create"},
		{"help", "chat:completions"},
		{"audio:transcriptions", "help", "create"},
	} {
		got := runMainDispatch(t, "bash", append([]string{"openai"}, args...)...)
		if got.code != 0 || got.stderr != "" || got.stdout == "" {
			t.Fatalf("compatibility help failed: %+v", got)
		}
	}
	for _, hidden := range []string{"__complete", "@completion", "@manpages"} {
		got := runMainDispatch(t, "bash", "openai", "help", hidden)
		if got.code == 0 || !strings.Contains(got.stderr, "Unknown help topic") {
			t.Fatalf("internal command exposed as a help topic: %+v", got)
		}
	}
	got := runMainDispatch(t, "zsh", "openai", "__complete", "--", "")
	if strings.Contains(got.stdout, "admin:organization") || !strings.Contains(got.stdout, "admin") {
		t.Fatalf("root completion does not prefer subgroups: %+v", got)
	}
}

func TestMainCommandSubgroupsLegacyPrefixCompletion(t *testing.T) {
	summaries := map[string]string{
		"audio":                         "Transcribe audio, generate speech, and create voices.",
		"audio:transcriptions":          "Convert audio to text.",
		"audio:translations":            "Translate supported audio to English text.",
		"admin:organization:audit-logs": "List organization actions and configuration changes.",
		"beta:threads:runs":             "Create and manage runs on a beta thread.",
		"beta:threads:runs:steps":       "Inspect the steps of a beta thread run.",
	}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		t.Run(style, func(t *testing.T) {
			for _, tc := range []struct {
				args           []string
				want, bashWant string
				code           int
			}{
				{[]string{"audio:t"}, "audio:transcriptions\naudio:translations\n", "transcriptions\ntranslations\n", 0},
				{[]string{"audio", ":", "t"}, "audio:transcriptions\naudio:translations\n", "transcriptions\ntranslations\n", 0},
				{[]string{"admin:organization:audit"}, "admin:organization:audit-logs\n", "audit-logs\n", 0},
				{[]string{"admin", ":", "organization", ":", "audit"}, "admin:organization:audit-logs\n", "audit-logs\n", 0},
				{[]string{"beta:threads:r"}, "beta:threads:runs\nbeta:threads:runs:steps\n", "runs\nruns:steps\n", 0},
				{[]string{"audio:unknown"}, "", "", 0},
				{[]string{"au"}, "audio\n", "audio\n", 0},
				{[]string{"__"}, "", "", 0},
				{[]string{"@comp"}, "", "", 0},
				{[]string{"--format", "audio:t"}, "", "", 11},
				{[]string{"--mtls-client-cert-file", "audio:t"}, "", "", 10},
			} {
				t.Run(strings.Join(tc.args, "/"), func(t *testing.T) {
					want := tc.want
					if style == "bash" {
						want = tc.bashWant
					} else if (style == "zsh" || style == "fish") && want != "" {
						var records []string
						for _, name := range strings.Split(strings.TrimSuffix(want, "\n"), "\n") {
							if style == "zsh" {
								records = append(records, strings.ReplaceAll(name, ":", `\:`)+":"+summaries[name])
							} else {
								records = append(records, name+"\t"+summaries[name])
							}
						}
						want = strings.Join(records, "\n") + "\n"
					}
					args := append([]string{"openai", "__complete", "--"}, tc.args...)
					got := runMainDispatch(t, style, args...)
					if got != (mainDispatchResult{code: tc.code, stdout: want}) {
						t.Fatalf("legacy prefix completion: got %+v, want exit %d stdout %q and empty stderr", got, tc.code, want)
					}
				})
			}
		})
	}
}

func TestMainCommandSubgroupsPreserveRequestsAndOutput(t *testing.T) {
	const body = `{"object":"list","data":[{"id":"audit_synthetic","type":"project.created","effective_at":123}],"has_more":false}`
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.URL.Path != "/organization/audit_logs" || r.URL.Query().Get("limit") != "2" || r.Header.Get("Authorization") != "Bearer synthetic-admin-key" || r.Header.Get("OpenAI-Project") != "synthetic-project" {
			t.Errorf("nested request changed: %s %s auth=%q project=%q", r.Method, r.URL.RequestURI(), r.Header.Get("Authorization"), r.Header.Get("OpenAI-Project"))
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	defer server.Close()
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-user-key", "OPENAI_ADMIN_KEY=synthetic-admin-key"}
	for _, flags := range [][]string{nil, {"--format", "json"}, {"--format", "raw"}, {"--transform", "id", "--raw-output"}} {
		old := runMainDispatchWithEnv(t, "bash", env, append([]string{"openai", "--project", "synthetic-project", "admin:organization:audit-logs", "list", "--limit", "2"}, flags...)...)
		for _, path := range [][]string{
			{"admin", "organization", "audit-logs", "list"},
			{"admin", "--format", "auto", "organization", "audit-logs", "list"},
			{"admin", "organization", "audit-logs", "--format", "auto", "list"},
		} {
			args := append([]string{"openai", "--project", "synthetic-project"}, path...)
			args = append(args, "--limit", "2")
			got := runMainDispatchWithEnv(t, "bash", env, append(args, flags...)...)
			if got != old || got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "audit_synthetic") {
				t.Fatalf("request/output mismatch: old=%+v nested=%+v", old, got)
			}
		}
	}
	if requests.Load() != 16 {
		t.Fatalf("expected 16 requests, got %d", requests.Load())
	}
}

func TestMainCommandSubgroupsPreserveAudioAndErrors(t *testing.T) {
	server := readableAudioServer(t, "audio:transcriptions", "text/plain", "Synthetic transcript.\n")
	args := readableAudioArgs(t, "audio:transcriptions", "--response-format", "text")
	old := runReadableCommand(t, server, args...)
	nested := append([]string{"audio", "transcriptions"}, args[1:]...)
	got := runReadableCommand(t, server, nested...)
	if got != old || got.code != 0 || got.stdout != "Synthetic transcript.\n" {
		t.Fatalf("audio response capture changed: old=%+v nested=%+v", old, got)
	}
	for _, resource := range [][]string{{"audio:transcriptions"}, {"audio", "transcriptions"}} {
		command := append(append([]string{}, resource...), args[1:]...)
		failure := runMainCommandAPIErrorResponse(t, 400, "application/json", `{"error":{"message":"synthetic","type":"invalid_request_error","code":"invalid_value","param":"response_format"}}`, command)
		if failure.code == 0 || failure.stdout != "" || !strings.Contains(failure.stderr, "json, text, srt") || !strings.Contains(failure.stderr, "help --all "+strings.Join(resource, " ")+" create") {
			t.Fatalf("audio error context changed: %+v", failure)
		}
	}
	for _, command := range [][]string{{"beta:threads:runs", "retrieve"}, {"beta", "threads", "runs", "retrieve"}} {
		got := runMainDispatch(t, "bash", append([]string{"openai"}, command...)...)
		if got.code == 0 || got.stdout != "" || !strings.Contains(got.stderr, "thread-id") || !strings.Contains(got.stderr, "run-id") {
			t.Fatalf("required flags no longer fail: %+v", got)
		}
	}
	got = runMainDispatch(t, "bash", "openai", "admin", "organizatio")
	if got.code == 0 || !strings.Contains(got.stderr, "openai admin organization") {
		t.Fatalf("nested typo suggestion failed: %+v", got)
	}
}

func TestMainCommandSubgroupsPreserveBodyFlagScope(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body struct {
			Email, Role string
			Projects    []struct{ ID, Role string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if r.Method != "POST" || r.URL.Path != "/organization/invites" || body.Email != "synthetic@example.invalid" || body.Role != "reader" || len(body.Projects) != 1 || body.Projects[0].ID != "project_synthetic" || body.Projects[0].Role != "member" {
			t.Errorf("request body/route changed: %s %s %+v", r.Method, r.URL.Path, body)
		}
		if r.Header.Get("Authorization") != "Bearer synthetic-admin-key" {
			t.Errorf("admin credential selection changed")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"invite_synthetic","object":"organization.invite"}`)
	}))
	defer server.Close()
	for _, route := range [][]string{{"admin:organization:invites"}, {"admin", "organization", "invites"}} {
		args := append([]string{"openai", "--format", "json"}, route...)
		args = append(args, "create", "--email", "synthetic@example.invalid", "--role", "reader", "--project", `{"id":"project_synthetic","role":"member"}`)
		got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_ADMIN_KEY=synthetic-admin-key"}, args...)
		if got.code != 0 || got.stderr != "" || !strings.Contains(got.stdout, "invite_synthetic") {
			t.Fatalf("request-local project flag failed: %+v", got)
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("wanted two equivalent requests, got %d", requests.Load())
	}
}

func TestMainCommandSubgroupsStreamingStaysIncremental(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/chat/completions" {
			t.Errorf("wrong stream route: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeStreamingTextEvent(w, `{"id":"chat_live","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		writeStreamingTextEvent(w, `{"id":"chat_live","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`)
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	args := streamingTextArgs("chat:completions")
	args = append([]string{"chat", "completions"}, args[1:]...)
	child, stdout, stderr, ctx := startStreamingTextCommand(t, server, args...)
	readStreamingTextPrefix(t, ctx, stdout, "Hello")
	release <- struct{}{}
	rest, err := io.ReadAll(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil || stderr.Len() != 0 || string(rest) != " world\n" {
		t.Fatalf("nested streaming failed: %v stderr=%q rest=%q", err, stderr.String(), rest)
	}
}
