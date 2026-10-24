package mocktest

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var mockServerURL *url.URL

func init() {
	mockServerURL, _ = url.Parse("http://localhost:4010")
	if testURL := os.Getenv("TEST_API_BASE_URL"); testURL != "" {
		if parsed, err := url.Parse(testURL); err == nil {
			mockServerURL = parsed
		}
	}
}

// OnlyMockServerDialer only allows network connections to the mock server
type OnlyMockServerDialer struct{}

func (d *OnlyMockServerDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if address == mockServerURL.Host {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}

	return nil, fmt.Errorf("BLOCKED: connection to %s not allowed (only allowed: %s)", address, mockServerURL.Host)
}

func blockNetworkExceptMockServer() (http.RoundTripper, http.RoundTripper) {
	restricted := &http.Transport{
		DialContext: (&OnlyMockServerDialer{}).DialContext,
	}

	origClient, origDefault := http.DefaultClient.Transport, http.DefaultTransport
	http.DefaultClient.Transport, http.DefaultTransport = restricted, restricted
	return origClient, origDefault
}

func restoreNetwork(origClient, origDefault http.RoundTripper) {
	http.DefaultClient.Transport, http.DefaultTransport = origClient, origDefault
}

// TestRunMockTestWithFlags runs a test against a mock server with the provided
// CLI args and ensures it succeeds
func TestRunMockTestWithFlags(t *testing.T, args ...string) {
	t.Helper()
	TestRunMockTestWithPipeAndFlags(t, nil, args...)
}

// TestRunMockTestWithPipeAndFlags runs a test against a mock server with the provided
// data piped over stdin and CLI args and ensures it succeeds
func TestRunMockTestWithPipeAndFlags(t *testing.T, pipeData []byte, args ...string) {
	t.Helper()
	if testRetiredCommand(t, pipeData, args) {
		return
	}
	origClient, origDefault := blockNetworkExceptMockServer()
	defer restoreNetwork(origClient, origDefault)

	// Check if mock server is running
	conn, err := net.DialTimeout("tcp", mockServerURL.Host, 2*time.Second)
	if err != nil {
		require.Fail(t, "Mock server is not running on "+mockServerURL.Host+". Please start the mock server before running tests.")
	} else {
		conn.Close()
	}

	if suiteCLI == nil {
		t.Fatal("mock CLI tests must call mocktest.RunTests from TestMain")
	}
	binary, err := suiteCLI(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	args = append([]string{"--base-url", mockServerURL.String()}, args...)
	t.Logf("Testing command: openai %s", strings.Join(args, " "))
	cmd := exec.CommandContext(t.Context(), binary, args...)
	cmd.Stdin = bytes.NewReader(pipeData)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI command %q failed: %v\nOutput: %s", args, err, output)
	}

	t.Logf("Test passed successfully\nOutput:\n%s", string(output))
}

func TestFile(t *testing.T, contents string) string {
	tmpDir := t.TempDir()
	filename := filepath.Join(tmpDir, "file.txt")
	require.NoError(t, os.WriteFile(filename, []byte(contents), 0644))
	return filename
}
