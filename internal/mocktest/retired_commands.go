package mocktest

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Generated API fixtures outlive CLI resource retirement. Exercise their actual
// invocations as rejection tests until generation removes those fixtures.
func testRetiredCommand(t *testing.T, input []byte, args []string) bool {
	t.Helper()
	retired := false
	helpTopic := ""
	for index, argument := range args {
		if strings.HasPrefix(argument, "fine-tuning:") ||
			(argument == "models" && index+1 < len(args) && args[index+1] == "delete") {
			retired = true
			if argument == "models" {
				helpTopic = " models"
			}
			break
		}
	}
	if !retired {
		return false
	}
	if suiteCLI == nil {
		t.Fatal("mock CLI tests must call mocktest.RunTests from TestMain")
	}
	binary, err := suiteCLI(t.Context())
	require.NoError(t, err)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, append([]string{"--base-url", server.URL}, args...)...)
	// Use the public invocation name instead of the temporary binary's path.
	command.Args[0] = "openai"
	command.Env = []string{"HOME=" + t.TempDir(), "SystemRoot=" + os.Getenv("SystemRoot"),
		"OPENAI_CLI_NO_UPDATE_CHECK=1", "OPENAI_CLI_NO_AUTO_UPDATE=1"}
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	require.NoError(t, ctx.Err())
	require.Empty(t, stdout.String())
	message := stderr.String()
	if strings.HasPrefix(message, "Unknown command.") {
		require.Equal(t, 3, exit.ExitCode())
		require.Equal(t, "Unknown command. Run openai help"+helpTopic+" to see commands.\n", message)
	} else {
		// Root flag validation precedes unknown-command dispatch. Removed
		// operation flags therefore retain the ordinary local option error.
		firstLine, _, _ := strings.Cut(message, "\n")
		unknownOption := firstLine == "An option is not recognized." ||
			firstLine == "The --model option is not available for this command." ||
			firstLine == "The --max-items option is not available for this command."
		require.True(t, unknownOption, "unexpected rejection: %s", message)
		require.Equal(t, 1, exit.ExitCode())
		require.Equal(t, firstLine+"\nOptions and examples: openai"+helpTopic+" --help\n", message)
	}
	require.Zero(t, requests.Load(), "retired command sent an API request")
	return true
}
