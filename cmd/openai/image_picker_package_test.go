package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMainPackagePickerIgnoresRequestConfiguration(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "openai")
	goBinary := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		binary += ".exe"
		goBinary += ".exe"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, goBinary, "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	for _, scenario := range []struct {
		name string
		env  []string
	}{
		{"bad-base", []string{"OPENAI_BASE_URL=invalid-synthetic-url"}},
		{"missing-mtls-pair", []string{"OPENAI_BASE_URL=https://127.0.0.1:1", "OPENAI_MTLS_CLIENT_CERT_FILE=" + filepath.Join(root, "missing-cert"), "OPENAI_MTLS_CLIENT_KEY_FILE=" + filepath.Join(root, "missing-key")}},
		{"partial-mtls", []string{"OPENAI_MTLS_CLIENT_CERT_FILE=" + filepath.Join(root, "missing-cert")}},
		{"partial-mtls-key", []string{"OPENAI_MTLS_CLIENT_KEY_FILE=" + filepath.Join(root, "missing-key")}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			environment := []string{}
			for _, entry := range os.Environ() {
				name, _, _ := strings.Cut(entry, "=")
				if !strings.HasPrefix(strings.ToUpper(name), "OPENAI_") {
					environment = append(environment, entry)
				}
			}
			environment = append(environment, "HOME="+root, "USERPROFILE="+root, "OPENAI_API_KEY=fake-package-test-key")
			environment = append(environment, scenario.env...)
			for _, arguments := range [][]string{
				{"@completion", "fish", "--package-picker"},
				{"--format-error", "json", "@completion", "fish", "--package-picker=true"},
			} {
				command := exec.CommandContext(ctx, binary, arguments...)
				command.Env = environment
				var output, diagnostics bytes.Buffer
				command.Stdout, command.Stderr = &output, &diagnostics
				if err := command.Run(); err != nil || diagnostics.Len() != 0 || !strings.Contains(output.String(), "image-picker.json.tab-off-fish") {
					t.Fatalf("local %v generation failed: %v; stderr=%q", arguments, err, diagnostics.String())
				}
			}
			// The same flag-looking string as a request value never bypasses
			// base URL or mTLS validation. No live server or valid key is used.
			for _, token := range []string{"--package-picker"} {
				command := exec.CommandContext(ctx, binary, "images", "generate", "--model", "gpt-image-1", "--prompt", token)
				command.Env = environment
				var output, diagnostics bytes.Buffer
				command.Stdout, command.Stderr = &output, &diagnostics
				if err := command.Run(); err == nil || output.Len() != 0 || (!strings.Contains(diagnostics.String(), "OPENAI_BASE_URL") && !strings.Contains(diagnostics.String(), "mTLS")) {
					t.Fatalf("request configuration validation changed for %s: %v; stderr=%q", token, err, diagnostics.String())
				}
			}
		})
	}
}
