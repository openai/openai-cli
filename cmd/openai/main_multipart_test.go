package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMainMultipartRedirectPrivacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(path, []byte("synthetic upload"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprintf("status_%d/debug_%t", status, debug), func(t *testing.T) {
				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Location", "https://fake-user:fake-password@example.invalid/fake-capability?signature=fake-signature#fake-fragment")
					w.WriteHeader(status)
				}))
				t.Cleanup(server.Close)
				args := []string{"openai", "--base-url", server.URL, "--api-key", "fake-test-key"}
				if debug {
					args = append(args, "--debug")
				}
				args = append(args, "files", "create", "--file", path, "--purpose", "assistants")
				got := runMainDispatch(t, "", args...)
				want := fmt.Sprintf("cannot follow HTTP %d redirect: streamed multipart uploads are not replayable\n", status)
				if got.code == 0 || !strings.Contains(got.stderr, want) {
					t.Errorf("main redirect status %d: got %+v, want nonzero exit and stderr containing %q", status, got, want)
				}
				for _, sensitive := range []string{"fake-user", "fake-password", "example.invalid", "fake-capability", "fake-signature", "fake-fragment"} {
					if strings.Contains(got.stderr+got.stdout, sensitive) {
						t.Errorf("main redirect status %d: got sensitive marker %q in output, want it omitted", status, sensitive)
					}
				}
				if count := requests.Load(); count != 1 {
					t.Errorf("main redirect status %d: got %d requests, want 1", status, count)
				}
			})
		}
	}
}
