package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const costRecoveryValidAdmin = "synthetic-recovery-valid-admin"
const costRecoveryInvalidAdmin = "synthetic-recovery-invalid-admin"

func costRecoveryContextArgs(endpoint string) []string {
	return []string{"--base-url", endpoint, "--organization", "org_recovery", "--project", "proj_header",
		"--header", "X-Cost-Recovery: preserved", "--project-id", "proj_scope", "--project-id", "proj_other"}
}

func assertCostRecoveryContext(t *testing.T, request *http.Request) {
	t.Helper()
	assert.Equal(t, http.MethodGet, request.Method)
	assert.Equal(t, "/recovery-context/v1/organization/costs", request.URL.Path)
	assert.Equal(t, "org_recovery", request.Header.Get("OpenAI-Organization"))
	assert.Equal(t, "proj_header", request.Header.Get("OpenAI-Project"))
	assert.Equal(t, "preserved", request.Header.Get("X-Cost-Recovery"))
	query := request.URL.Query()
	assert.Equal(t, costReportStart, query.Get("start_time"))
	assert.Equal(t, costReportEnd, query.Get("end_time"))
	assert.Equal(t, []string{"proj_scope", "proj_other"}, query["project_ids[]"])
	assert.Equal(t, []string{"project_id"}, query["group_by[]"])
	assert.Equal(t, "1d", query.Get("bucket_width"))
	assert.Equal(t, "180", query.Get("limit"))
}

func TestMainCostReportAuthRecoveryPreservesExplicitContext(t *testing.T) {
	const details = `{"message":"synthetic access rejection","type":"permission_error","code":"synthetic_access","param":null,"future":{"sequence":9007199254740993}}`
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assertCostRecoveryContext(t, r)
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") != "Bearer "+costRecoveryValidAdmin {
					w.Header().Set("X-Should-Retry", "false")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"error":`+details+`}`)
					return
				}
				_, _ = io.WriteString(w, costReportPage(`{"object":"organization.costs.result","project_id":"proj_scope","amount":{"currency":"usd","value":12.34}}`, "", false))
			}))
			t.Cleanup(server.Close)
			args := append(costReportArgs(), costRecoveryContextArgs(server.URL+"/recovery-context/v1")...)
			invalid := append(slices.Clone(args), "--admin-api-key", costRecoveryInvalidAdmin)
			for _, environmentAdmin := range []string{costRecoveryInvalidAdmin, costRecoveryValidAdmin} {
				got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_ADMIN_KEY=" + environmentAdmin}, invalid...)
				require.Equal(t, 1, got.code, "%+v", got)
				require.Empty(t, got.stdout)
				require.Contains(t, got.stderr, "--admin-api-key")
				require.Contains(t, got.stderr, "OPENAI_ADMIN_KEY")
				for _, value := range []string{costRecoveryInvalidAdmin, costRecoveryValidAdmin} {
					require.NotContains(t, got.stderr, value)
				}
			}
			// Follow the explicit-override guidance without dropping any request context.
			fixed := append(slices.Clone(args), "--admin-api-key", costRecoveryValidAdmin)
			got := runMainDispatchWithEnv(t, "bash", []string{"OPENAI_ADMIN_KEY=" + costRecoveryInvalidAdmin}, fixed...)
			require.Zero(t, got.code, "%+v", got)
			require.Empty(t, got.stderr)
			require.Contains(t, got.stdout, "12.34")

			structured := append(slices.Clone(invalid), "--format", "json")
			got = runMainDispatchWithEnv(t, "bash", []string{"OPENAI_ADMIN_KEY=" + costRecoveryValidAdmin}, structured...)
			require.Equal(t, 1, got.code)
			require.Empty(t, got.stdout)
			require.Equal(t, decodeMainErrorObject(t, "json", details), decodeMainErrorObject(t, "json", got.stderr))
			require.NotContains(t, got.stderr, "--admin-api-key")
			require.NotContains(t, got.stderr, "OPENAI_ADMIN_KEY")
			require.EqualValues(t, 4, requests.Load())
		})
	}
}

func TestMainCostReportExpansionRecoveryThroughNativeShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this focused recovery check uses native Bash and zsh")
	}
	// Reuse the production-main subprocess harness instead of building another CLI.
	// The launcher gives copied help a real executable path with shell metacharacters.
	process, err := os.Executable()
	require.NoError(t, err)
	work := filepath.Join(t.TempDir(), "CLI with spaces & apostrophe's")
	require.NoError(t, os.Mkdir(work, 0o700))
	launcher := filepath.Join(work, "openai")
	script := "#!/bin/sh\nOPENAI_CLI_MAIN_DISPATCH_PROCESS=1 exec " + costRecoveryQuote(process) +
		" -test.run='^TestMainDispatchProcess$' -- \"$0\" \"$@\"\n"
	require.NoError(t, os.WriteFile(launcher, []byte(script), 0o700))
	elsewhere := t.TempDir()
	decoy := filepath.Join(elsewhere, "openai")
	require.NoError(t, os.WriteFile(decoy, []byte("#!/bin/sh\nprintf 'wrong executable\\n'\nexit 99\n"), 0o700))
	required := strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ",")
	for _, shell := range []nativeShell{
		{name: "bash", executable: "bash", args: []string{"--noprofile", "--norc", "-c"}},
		{name: "zsh", executable: "zsh", args: []string{"-f", "-c"}},
	} {
		t.Run(shell.name, func(t *testing.T) {
			path, err := exec.LookPath(shell.executable)
			if err != nil {
				if slices.Contains(required, shell.name) {
					t.Fatalf("required native shell %s is unavailable: %v", shell.name, err)
				}
				t.Skipf("%s is unavailable", shell.name)
			}
			shell.executable = path
			t.Setenv("PATH", elsewhere+string(os.PathListSeparator)+os.Getenv("PATH"))
			first := costReportPage(`{"object":"organization.costs.result","project_id":"proj_scope","amount":{"currency":"usd","value":1}},{"object":"organization.costs.result","project_id":"proj_scope","amount":{"currency":"usd","value":1e1048577}}`, "raw-next-page", true)
			last := costReportPage(`{"object":"organization.costs.result","project_id":"proj_other","amount":{"currency":"usd","value":0.00000000000000000001},"future":9007199254740993}`, "", false)
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assertCostRecoveryContext(t, r)
				assert.Equal(t, "Bearer "+costRecoveryValidAdmin, r.Header.Get("Authorization"))
				w.Header().Set("Content-Type", "application/json")
				if cursor := r.URL.Query().Get("page"); cursor != "" {
					assert.Equal(t, "raw-next-page", cursor)
					_, _ = io.WriteString(w, last)
					return
				}
				_, _ = io.WriteString(w, first)
			}))
			t.Cleanup(server.Close)
			endpoint := server.URL + "/recovery-context/v1"
			contextArgs := append(costRecoveryContextArgs(endpoint), "--admin-api-key", costRecoveryValidAdmin)
			quotedContext := ""
			for _, argument := range contextArgs {
				quotedContext += " " + costRecoveryQuote(argument)
			}
			home := t.TempDir()
			invocation := costRecoveryQuote(launcher)
			report := invocation + " costs report --from " + costReportFrom + " --to " + costReportTo + quotedContext
			got := runNativeShell(t, shell, elsewhere, home, endpoint, report)
			require.Equal(t, 1, got.code, "%+v", got)
			require.Empty(t, got.stdout)
			require.EqualValues(t, 1, requests.Load())
			for _, instruction := range []string{"No report was written", "--start-time " + costReportStart,
				"--end-time " + costReportEnd, "--group-by project_id", "--format raw", "project", "request",
				"next_page", "--page", "has_more"} {
				require.Contains(t, got.stderr, instruction)
			}
			require.NotContains(t, got.stderr, costRecoveryValidAdmin)
			var copied string
			for _, line := range strings.Split(got.stderr, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasSuffix(line, " admin organization usage costs --help") {
					copied = line
					break
				}
			}
			require.Equal(t, invocation+" admin organization usage costs --help", copied)
			help := runNativeShell(t, shell, elsewhere, home, endpoint, copied)
			require.Zero(t, help.code, "%+v", help)
			require.Empty(t, help.stderr)
			require.Contains(t, help.stdout, "--start-time")
			require.NotContains(t, help.stdout, "wrong executable")
			require.EqualValues(t, 1, requests.Load(), "copied help must not repeat the request")

			// Follow the manual fallback with the same filters and request settings.
			raw := strings.TrimSuffix(copied, " --help") + " --start-time " + costReportStart +
				" --end-time " + costReportEnd + " --group-by project_id --bucket-width 1d --limit 180 --format raw" + quotedContext
			page := runNativeShell(t, shell, elsewhere, home, endpoint, raw)
			require.Zero(t, page.code, "%+v", page)
			require.Empty(t, page.stderr)
			require.Equal(t, first+"\n", page.stdout)
			require.EqualValues(t, 2, requests.Load(), "the raw route returns exactly one page")
			page = runNativeShell(t, shell, elsewhere, home, endpoint, raw+" --page raw-next-page")
			require.Zero(t, page.code, "%+v", page)
			require.Empty(t, page.stderr)
			require.Equal(t, last+"\n", page.stdout)
			require.EqualValues(t, 3, requests.Load())
		})
	}
}

func costRecoveryQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
