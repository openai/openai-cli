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
	for _, test := range []struct {
		name          string
		sourceFailure bool
	}{{"complete sources", false}, {"partial source failure", true}} {
		t.Run(test.name, func(t *testing.T) {
			server, requests := newLimitsWorkflowServer(t, func(r *http.Request) (int, string) {
				if test.sourceFailure && r.URL.Path == "/organization/spend_alerts" {
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
			defer func() {
				if child.ProcessState == nil {
					_ = child.Process.Kill()
					_ = child.Wait()
				}
			}()
			if _, err := io.ReadFull(stdout, make([]byte, 1)); err != nil {
				t.Fatal(err)
			}
			if err := stdout.Close(); err != nil {
				t.Fatal(err)
			}
			err = child.Wait()
			if ctx.Err() != nil || requests.count("/organization/spend_alerts") != 1 {
				t.Fatalf("closed-pipe command timed out or repeated a source: context=%v stderr=%q", ctx.Err(), stderr.String())
			}
			if !test.sourceFailure {
				if err != nil || stderr.Len() != 0 {
					t.Fatalf("lone output EPIPE changed success: error=%v stderr=%q", err, stderr.String())
				}
				return
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("source failure disappeared after EPIPE: error=%v stderr=%q", err, stderr.String())
			}
			if !json.Valid(stderr.Bytes()) || !strings.Contains(stderr.String(), "overview is incomplete") || strings.Contains(stderr.String(), "private synthetic") {
				t.Fatalf("safe structured recovery missing: %q", stderr.String())
			}
		})
	}
}
