package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestMainBatchesOutputHelperRejectsInvalidInvocation(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "private output helper must stay offline", http.StatusInternalServerError)
	}))
	defer server.Close()

	cases := []struct {
		name string
		args []string
	}{
		{"missing", nil},
		{"extra", []string{"3", "extra"}},
		{"empty", []string{""}},
		{"malformed", []string{"not-a-handle"}},
		{"negative", []string{"-1"}},
		{"stdin", []string{"0"}},
		{"stdout", []string{"1"}},
		{"stderr", []string{"2"}},
		{"uint64_overflow", []string{"18446744073709551616"}},
	}
	if strconv.IntSize == 32 {
		cases = append(cases, struct {
			name string
			args []string
		}{"uintptr_overflow", []string{"4294967296"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Run main in an isolated process: a helper must never claim or close
			// a descriptor owned by this test process during malformed input checks.
			args := append([]string{"openai", "__batch-output"}, tc.args...)
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_BASE_URL=" + server.URL}, args...)
			if got.code != 1 || got.stdout != "" || got.stderr != "" || requests.Load() != 0 {
				t.Fatalf("invalid helper invocation changed dispatch: result=%+v requests=%d", got, requests.Load())
			}
		})
	}
}
