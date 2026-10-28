package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainFineTuningCommandsAreUnavailable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	env := []string{"OPENAI_BASE_URL=" + server.URL, "OPENAI_API_KEY=synthetic-project-key", "OPENAI_ADMIN_KEY=synthetic-admin-key"}
	const unknownCommand = "Unknown command. Run openai help to see commands."
	const rootHelp = "\nOptions and examples: openai --help"
	for _, test := range []struct {
		resource string
		args     []string
		code     int
		message  string
	}{
		{"jobs", []string{"create", "--model", "model_synthetic", "--training-file", "file_synthetic"}, 1, "The --model option is not available for this command." + rootHelp},
		{"jobs", []string{"retrieve", "ftjob_synthetic"}, 3, unknownCommand},
		{"jobs", []string{"list"}, 3, unknownCommand},
		{"jobs", []string{"cancel", "ftjob_synthetic"}, 3, unknownCommand},
		{"jobs", []string{"list-events", "ftjob_synthetic"}, 3, unknownCommand},
		{"jobs", []string{"pause", "ftjob_synthetic"}, 3, unknownCommand},
		{"jobs", []string{"resume", "ftjob_synthetic"}, 3, unknownCommand},
		{"jobs checkpoints", []string{"list", "ftjob_synthetic"}, 3, unknownCommand},
		{"checkpoints permissions", []string{"create", "ft_synthetic", "--project-id", "proj_synthetic"}, 1, "The --project-id option is not available for this command." + rootHelp},
		{"checkpoints permissions", []string{"retrieve", "ft_synthetic"}, 3, unknownCommand},
		{"checkpoints permissions", []string{"list", "ft_synthetic"}, 3, unknownCommand},
		{"checkpoints permissions", []string{"delete", "ft_synthetic", "perm_synthetic"}, 3, unknownCommand},
		{"alpha graders", []string{"run", "--grader", `{"type":"string_check","name":"Synthetic","input":"{{sample.output_text}}","reference":"synthetic","operation":"eq"}`, "--model-sample", "synthetic-private-sample"}, 1, "An option is not recognized." + rootHelp},
		{"alpha graders", []string{"validate", "--grader", `{"type":"string_check","name":"Synthetic","input":"{{sample.output_text}}","reference":"synthetic","operation":"eq"}`}, 1, "An option is not recognized." + rootHelp},
	} {
		nested := strings.Fields("fine-tuning " + test.resource)
		for _, route := range [][]string{nested, {strings.Join(nested, ":")}} {
			// Unsupported options fail during parsing before unknown-command routing.
			// Probe every operation without options to verify the route itself is absent.
			for _, probe := range []struct {
				name, message string
				args          []string
				code          int
			}{
				{"without options", unknownCommand, test.args[:1], 3},
				{"with inputs", test.message, test.args, test.code},
			} {
				for _, format := range []string{"text", "json"} {
					t.Run(strings.Join(route, "/")+"/"+test.args[0]+"/"+probe.name+"/"+format, func(t *testing.T) {
						args := append([]string{"openai", "--format-error", format}, route...)
						args = append(args, probe.args...)
						got := runMainDispatchWithEnv(t, "bash", env, args...)
						if got.code != probe.code || got.stdout != "" {
							t.Fatalf("removed command = %+v; want exit %d and empty stdout", got, probe.code)
						}
						message := strings.TrimSpace(got.stderr)
						if format == "json" {
							payload := decodeMainStructuredError(t, format, got.stderr)
							message, _ = payload["message"].(string)
							if _, exists := payload["status_code"]; exists {
								t.Error("removed command invented an HTTP status")
							}
						}
						if message != probe.message {
							t.Errorf("removed command diagnostic = %q; want %q", message, probe.message)
						}
						if strings.Contains(got.stdout+got.stderr, "synthetic-") {
							t.Error("removed command exposed a synthetic operand or credential")
						}
						if count := requests.Load(); count != 0 {
							t.Fatalf("removed commands sent %d requests; want 0", count)
						}
					})
				}
			}
		}
	}
}

func TestMainFineTuningHelpTopicsAreUnavailable(t *testing.T) {
	for _, path := range []string{
		"fine-tuning", "fine-tuning jobs", "fine-tuning jobs checkpoints", "fine-tuning checkpoints",
		"fine-tuning checkpoints permissions", "fine-tuning alpha", "fine-tuning alpha graders",
	} {
		nested := strings.Fields(path)
		routes := [][]string{nested}
		if len(nested) > 1 {
			routes = append(routes, []string{strings.Join(nested, ":")})
		}
		for _, route := range routes {
			for _, full := range []bool{false, true} {
				args := append([]string{"openai"}, route...)
				args = append(args, "--help")
				if full {
					args = append([]string{"openai", "help", "--all"}, route...)
				}
				got := runMainDispatch(t, "bash", args...)
				if got.code != 3 || got.stdout != "" || !strings.HasPrefix(got.stderr, "Unknown help topic.") ||
					!strings.Contains(got.stderr, "openai help") || strings.Contains(got.stderr, "fine-tuning") {
					t.Errorf("removed help %q = %+v", args, got)
				}
			}
		}
	}
}

func TestMainFineTuningIsAbsentFromDiscovery(t *testing.T) {
	for _, args := range [][]string{{"openai", "--help"}, {"openai", "help", "--all"}} {
		got := runMainDispatch(t, "bash", args...)
		if got.code != 0 || got.stderr != "" || strings.Contains(got.stdout, "fine-tuning") {
			t.Fatalf("root help retained fine-tuning: %+v", got)
		}
		for _, name := range []string{"models", "batches", "admin"} {
			if !strings.Contains(got.stdout, "  "+name+" ") {
				t.Errorf("root help lost %s: %s", name, got.stdout)
			}
		}
	}
	for _, style := range []string{"bash", "zsh", "fish", "pwsh"} {
		got := runMainDispatch(t, style, mainCompletionArgs(style, "")...)
		if got.code != 0 || got.stderr != "" || strings.Contains(got.stdout, "fine-tuning") {
			t.Fatalf("%s completion retained fine-tuning: %+v", style, got)
		}
		for _, name := range []string{"models", "batches", "admin"} {
			if !strings.Contains(got.stdout, name) {
				t.Errorf("%s completion lost %s: %s", style, name, got.stdout)
			}
		}
	}
	got := runMainDispatch(t, "bash", "openai", "explore", "fine-tuning")
	if got.code != 0 || got.stderr != "" || got.stdout != "No matching commands. Try fewer words.\n" {
		t.Fatalf("command search retained fine-tuning: %+v", got)
	}
}
