//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMainLimitsWorkflowPartialFailureSurvivesClosedPipe(t *testing.T) {
	server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
		if r.URL.Path == "/organization/spend_alerts" {
			return http.StatusForbidden, `{"error":{"message":"private synthetic source failure"}}`
		}
		status, body := limitsWorkflowSuccess(t, false, r)
		if r.URL.Path == "/organization/spend_limit" {
			body = strings.TrimSuffix(body, "}") + `,"future":"` + strings.Repeat("synthetic", 2<<20) + `"}`
		}
		return status, body
	})
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	child := batchesWorkflowProcess(t, ctx, server, "admin", "limits", "show", "--format", "json")
	child.Env = append(child.Env, "OPENAI_ADMIN_KEY=sk-fake-spend-admin")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(stdout, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	if err := stdout.Close(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 1 || ctx.Err() != nil || requests.count("/organization/spend_alerts") != 1 {
		t.Fatalf("source failure disappeared after EPIPE: error=%v context=%v stderr=%q", err, ctx.Err(), stderr.String())
	}
	if !json.Valid(stderr.Bytes()) || !strings.Contains(stderr.String(), "overview is incomplete") || strings.Contains(stderr.String(), "private synthetic") {
		t.Fatalf("safe structured recovery missing: %q", stderr.String())
	}
}
