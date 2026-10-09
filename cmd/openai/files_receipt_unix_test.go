//go:build !windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Native Windows terminal behavior needs a separate harness. These cases use
// Unix PTYs so both output streams exercise the public interactive presenter.
func TestMainFilesReceiptErrorOptionsIndependent(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for the Unix Files receipt PTY cases")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is required for the Unix Files receipt PTY cases")
	}
	const response = `{"id":"` + filesWorkflowID + `","object":"file","bytes":13,"created_at":1700000000,"filename":"upload space.txt","purpose":"user_data","status":"uploaded"}`
	const apiError = `{"message":"Synthetic upload rejected.","type":"invalid_request_error","code":"synthetic_file_error"}`
	const legacyMetadata = "ID: " + filesWorkflowID + "\nObject: file\nBytes: 13\nCreated at: 1700000000\n" +
		"Filename: upload space.txt\nPurpose: user_data\nStatus: uploaded\n"
	errorJSON := []string{"--format-error", "json"}
	errorTransform := []string{"--transform-error", "message"}
	errorBoth := []string{"--format-error", "json", "--transform-error", "message"}
	for _, tc := range []struct {
		name, route, output string
		flags               []string
		status              int
	}{
		{"upload default", "upload", "receipt", nil, http.StatusOK},
		{"upload error JSON", "upload", "receipt", errorJSON, http.StatusOK},
		{"upload error transform", "upload", "receipt", errorTransform, http.StatusOK},
		{"upload both error options", "upload", "receipt", errorBoth, http.StatusOK},
		{"quiet with error options", "upload", "metadata", append([]string{"--quiet"}, errorBoth...), http.StatusOK},
		{"success JSON with error options", "upload", "json", append([]string{"--format", "json"}, errorBoth...), http.StatusOK},
		{"raw extraction with error options", "upload", "raw", append([]string{"--transform", "id", "--raw-output"}, errorBoth...), http.StatusOK},
		{"legacy create default", "create", "legacy", nil, http.StatusOK},
		{"legacy create error JSON", "create", "legacy", errorJSON, http.StatusOK},
		{"legacy create error transform", "create", "legacy", errorTransform, http.StatusOK},
		{"legacy create both error options", "create", "legacy", errorBoth, http.StatusOK},
		{"HTTP 400 structured error", "upload", "error", errorJSON, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			const filename = "upload space.txt"
			payload := []byte("hello files!\n")
			require.NoError(t, os.WriteFile(filepath.Join(work, filename), payload, 0o600))
			body := response
			if tc.status != http.StatusOK {
				body = `{"error":` + apiError + `}`
			}
			server, requests := filesWorkflowUploadServerWithResponse(t, filename, payload, tc.status, body)
			args := []string{"openai", "files", tc.route}
			if tc.route == "create" {
				args = append(args, "--file")
			}
			args = append(args, filename, "--purpose", "user_data")
			args = append(args, tc.flags...)
			got := runFilesReceiptPTY(t, python, bash, work, server.URL, args)
			require.EqualValues(t, 1, requests.Load(), "each command must upload exactly once; result=%+v", got)
			if tc.output == "error" {
				require.Equal(t, 1, got.code)
				require.Empty(t, got.stdout)
				require.JSONEq(t, apiError, got.stderr, "the complete error stream must remain one JSON value")
				require.NotContains(t, got.stderr, "Uploaded ")
				require.NotContains(t, got.stderr, "Inspect it:")
				return
			}
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			if tc.output == "receipt" {
				want := "Uploaded upload space.txt (13 B)\nID: " + filesWorkflowID + "\nPurpose: user_data\n\n" +
					"Inspect it: openai files get " + filesWorkflowID + "\n"
				require.Equal(t, want, got.stdout, "error options must not change successful stdout")
				return
			}
			require.NotContains(t, got.stdout, "Uploaded ")
			require.NotContains(t, got.stdout, "Inspect it:")
			switch tc.output {
			case "json":
				require.JSONEq(t, response, got.stdout)
			case "raw":
				require.Equal(t, filesWorkflowID+"\n", got.stdout)
			case "metadata", "legacy":
				require.Equal(t, legacyMetadata, got.stdout, "keep the complete legacy metadata output")
			}
		})
	}
}

func runFilesReceiptPTY(t *testing.T, python, bash, work, endpoint string, args []string, environment ...map[string]string) mainDispatchResult {
	return runFilesReceiptPTYCommand(t, python, bash, work, endpoint, "", args, environment...)
}

func runFilesReceiptPTYCommand(t *testing.T, python, bash, work, endpoint, command string, args []string, environment ...map[string]string) mainDispatchResult {
	t.Helper()
	binary, err := os.Executable()
	require.NoError(t, err)
	var extraEnv map[string]string
	if len(environment) != 0 {
		extraEnv = environment[0]
	}
	input, err := json.Marshal(struct {
		Binary, Bash, Work, Endpoint, Command string
		Args                                  []string
		Env                                   map[string]string
	}{binary, bash, work, endpoint, command, args, extraEnv})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, python, "-c", filesReceiptPTYScript)
	// Keep Python and the public CLI isolated from personal credentials and hooks.
	child.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "LANG=en_US.UTF-8"}
	child.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	child.Cancel = func() error { return child.Process.Signal(syscall.SIGTERM) }
	child.WaitDelay = 2 * time.Second
	err = child.Run()
	require.NoError(t, ctx.Err(), "PTY harness timed out; stdout=%q stderr=%q", stdout.String(), stderr.String())
	require.NoError(t, err, "PTY harness failed; stdout=%q stderr=%q", stdout.String(), stderr.String())
	require.Empty(t, stderr.String())
	var result struct {
		Code           int
		Stdout, Stderr string
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &result), "invalid PTY harness result: %s", stdout.String())
	return mainDispatchResult{result.Code, result.Stdout, result.Stderr}
}

const filesReceiptPTYScript = `
import errno, json, os, pty, selectors, signal, subprocess, sys, time, tty

def interrupted(signum, frame):
    raise TimeoutError("Files receipt harness interrupted")

signal.signal(signal.SIGTERM, interrupted)
config = json.load(sys.stdin)
env = {
    "PATH": "/usr/bin:/bin", "HOME": config["Work"], "XDG_CONFIG_HOME": config["Work"],
    "LANG": "en_US.UTF-8", "TERM": "xterm-256color", "NO_COLOR": "1", "FORCE_COLOR": "0",
    "GOMAXPROCS": "2", "OPENAI_CLI_MAIN_DISPATCH_PROCESS": "1",
    "OPENAI_API_KEY": "sk-fake-files-receipt-test", "OPENAI_BASE_URL": config["Endpoint"],
}
env.update(config["Env"] or {})
selector = selectors.DefaultSelector()
masters, slaves, output = [], [], [bytearray(), bytearray()]
process = None
completed = False
try:
    for stream in range(2):
        master, slave = pty.openpty()
        masters.append(master)
        slaves.append(slave)
        tty.setraw(slave)
        os.set_blocking(master, False)
        selector.register(master, selectors.EVENT_READ, stream)
    # Keep Bash alive so the production presenter detects its actual caller.
    command = [config["Bash"], "--noprofile", "--norc", "-c",
               '"$@"\nresult=$?\nexit "$result"', "files-receipt-test",
               config["Binary"], "-test.run=^TestMainDispatchProcess$", "--", *(config["Args"] or [])]
    if config["Command"]:
        script = ('dispatch_binary=$1\n'
                  'openai() { "$dispatch_binary" -test.run=^TestMainDispatchProcess$ -- openai "$@"; }\n'
                  + config["Command"] + '\nresult=$?\nexit "$result"')
        command = [config["Bash"], "--noprofile", "--norc", "-c", script,
                   "files-receipt-test", config["Binary"]]
    process = subprocess.Popen(command, cwd=config["Work"], env=env, stdin=subprocess.DEVNULL,
                               stdout=slaves[0], stderr=slaves[1], start_new_session=True)
    for slave in slaves:
        os.close(slave)
    slaves.clear()
    deadline = time.monotonic() + 10
    while selector.get_map() or process.poll() is None:
        if time.monotonic() >= deadline:
            raise TimeoutError("Files receipt command exceeded ten seconds")
        for key, _ in selector.select(0.1):
            try:
                data = os.read(key.fd, 65536)
            except OSError as error:
                if error.errno == errno.EAGAIN:
                    continue
                if error.errno != errno.EIO:
                    raise
                data = b""
            if data:
                output[key.data].extend(data)
            else:
                selector.unregister(key.fd)
    code = process.wait(timeout=2)
    completed = True
finally:
    if process is not None and not completed:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait(timeout=2)
    selector.close()
    for descriptor in masters + slaves:
        os.close(descriptor)
print(json.dumps({"Code": code, "Stdout": output[0].decode("utf-8"), "Stderr": output[1].decode("utf-8")}))
`
