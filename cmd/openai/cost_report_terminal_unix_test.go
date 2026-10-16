//go:build !windows

package main

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMainCostReportCSVTerminalControlsKeepRedirectedBytes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for Unix cost-report PTY checks")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("Bash is required for Unix cost-report PTY checks")
	}
	const project = "=project\x1b]0;synthetic-title\x07\u009b31m\trow\nnext\rreturn"
	const currency = "+usd\x1b[31m\x07\u009dtitle\tcolumn\nrow\rreturn"
	const amount = "9007199254740993.000000000000000001"
	const terminalProject = `'=project\u001b]0;synthetic-title\u0007\u009b31m\trow\nnext\rreturn`
	const terminalCurrency = `'+usd\u001b[31m\u0007\u009dtitle\tcolumn\nrow\rreturn`
	projectJSON, err := json.Marshal(project)
	require.NoError(t, err)
	currencyJSON, err := json.Marshal(currency)
	require.NoError(t, err)
	response := costReportPage(`{"object":"organization.costs.result","project_id":`+string(projectJSON)+
		`,"amount":{"value":`+amount+`,"currency":`+string(currencyJSON)+`}}`, "", false)
	server, requests := costReportServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, response); err != nil {
			t.Errorf("write synthetic terminal Costs response: %v", err)
		}
	})
	args := costReportArgs("--export", "csv")
	wantRedirected := "project_id,currency,amount\n\"'" + project + "\",\"'" + currency + "\"," + amount + "\n"
	for _, tc := range []struct {
		name, term, noColor string
	}{
		{"xterm", "xterm-256color", ""},
		{"NO_COLOR", "xterm-256color", "1"},
		{"TERM dumb", "dumb", ""},
		{"TERM dumb and NO_COLOR", "dumb", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			environment := map[string]string{
				"OPENAI_ADMIN_KEY": "synthetic-cost-report-admin",
				"TERM":             tc.term, "NO_COLOR": tc.noColor, "FORCE_COLOR": "0",
			}
			before := requests.Load()
			// The existing helper gives stdout and stderr separate raw PTYs.
			terminal := runFilesReceiptPTY(t, python, bash, work, server.URL, args, environment)
			require.Zero(t, terminal.code, "%+v", terminal)
			require.Empty(t, terminal.stderr)
			rows, err := csv.NewReader(strings.NewReader(terminal.stdout)).ReadAll()
			require.NoError(t, err)
			require.Equal(t, [][]string{
				{"project_id", "currency", "amount"},
				{terminalProject, terminalCurrency, amount},
			}, rows, "terminal escaping must retain formula protection and the exact decimal amount")
			require.Equal(t, 2, strings.Count(terminal.stdout, "\n"), "API fields must not introduce terminal rows")
			for _, character := range terminal.stdout {
				if character < 0x20 && character != '\n' || character == 0x7f || character >= 0x80 && character <= 0x9f {
					t.Fatalf("terminal CSV contains an active control character: %U", character)
				}
			}
			require.Equal(t, before+1, requests.Load())

			// The same values keep their original bytes when stdout is a pipe.
			pipeEnv := append(costReportEnv(server), "TERM="+tc.term, "NO_COLOR="+tc.noColor)
			pipe := runMainDispatchWithEnv(t, "bash", pipeEnv, args...)
			require.Zero(t, pipe.code, "%+v", pipe)
			require.Empty(t, pipe.stderr)
			require.Equal(t, wantRedirected, pipe.stdout)
			require.Equal(t, before+2, requests.Load())

			// Bash keeps stderr on a PTY while redirecting only CSV stdout to a file.
			file := runFilesReceiptPTYCommand(t, python, bash, work, server.URL,
				"openai costs report --from 2026-10-01 --to 2026-10-08 --export csv > report.csv", nil, environment)
			require.Zero(t, file.code, "%+v", file)
			require.Empty(t, file.stdout)
			require.Empty(t, file.stderr)
			data, err := os.ReadFile(filepath.Join(work, "report.csv"))
			require.NoError(t, err)
			require.Equal(t, wantRedirected, string(data))
			require.Equal(t, before+3, requests.Load())
		})
	}
}
