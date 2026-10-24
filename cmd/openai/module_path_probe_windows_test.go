//go:build windows

package main

import (
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Temporary probe. Apply candidate.patch before compiling this file.
// The inherited label retains the old environment path through runNativeShell.
// Every script exits explicitly, so the label does not change exit behavior.
func TestMainNativeShellWindowsPowerShellModulePathProbe(t *testing.T) {
	lookupShell := func(t *testing.T, name string) string {
		t.Helper()
		path, err := exec.LookPath(name + ".exe")
		if err != nil {
			if slices.Contains(strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ","), name) {
				t.Fatalf("required shell %s is unavailable: %v", name, err)
			}
			t.Skipf("%s is not installed", name)
		}
		return path
	}
	powershell := lookupShell(t, "powershell")
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	t.Run("ambient-inherited", func(t *testing.T) {
		// Observe the real CI environment before installing the synthetic override.
		// This emits command identity, never environment values or module paths.
		shell := nativeShell{name: "powershell-inherited-control", executable: helper,
			args: []string{"-test.run=^TestShellFileWindowsProcessHelper$", "--", powershell,
				"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}}
		script := `try {
$command = Get-Command Out-File -ErrorAction Stop
[Console]::Out.WriteLine('ambient command=' + $command.Name + ' type=' + $command.CommandType + ' module=' + $command.ModuleName + ' version=' + $command.Module.Version + ' success=true')
exit 0
} catch {
[Console]::Out.WriteLine('ambient command=Out-File success=false error_type=' + $_.Exception.GetType().Name)
exit 75
}`
		got := runNativeShell(t, shell, t.TempDir(), t.TempDir(), "http://127.0.0.1:1", script)
		if len(got.stdout) > 512 || !strings.HasPrefix(got.stdout, "ambient command=Out-File ") || got.stderr != "" || (got.code != 0 && got.code != 75) {
			t.Fatalf("ambient observation did not return bounded metadata: exit=%d stdout_bytes=%d stderr_bytes=%d", got.code, len(got.stdout), len(got.stderr))
		}
		t.Logf("%s", strings.TrimSpace(got.stdout))
	})
	moduleRoot := t.TempDir()
	module := filepath.Join(moduleRoot, "Microsoft.PowerShell.Utility")
	if err := os.Mkdir(module, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"Microsoft.PowerShell.Utility.psd1": `@{
RootModule = 'Microsoft.PowerShell.Utility.psm1'
ModuleVersion = '99.0.0'
GUID = '22222222-3333-4444-5555-666666666666'
FunctionsToExport = @('Out-File')
CmdletsToExport = @()
AliasesToExport = @()
VariablesToExport = @()
}`,
		"Microsoft.PowerShell.Utility.psm1": `
[IO.File]::WriteAllText($env:SYNTHETIC_MODULE_PATH_MARKER, 'synthetic-out-file-import')
function Out-File {
    [CmdletBinding()]
    param([Parameter(ValueFromPipeline=$true)][object]$InputObject, [string]$FilePath, [switch]$Append)
    begin { throw 'SYNTHETIC_OUTFILE_SELECTED' }
}
`,
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(module, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("controlled module file=%s bytes=%d sha256=%x", name, len(source), sha256.Sum256([]byte(source)))
	}
	// This process-local override contains only the owned local fixture directory.
	// Windows PowerShell may append its own module directory during discovery.
	t.Setenv("PSModulePath", moduleRoot)
	for _, supervised := range []bool{false, true} {
		route := "direct"
		if supervised {
			route = "supervised"
		}
		for _, inherited := range []bool{true, false} {
			variant := "reset"
			if inherited {
				variant = "inherited"
			}
			t.Run(route+"/"+variant, func(t *testing.T) {
				work := t.TempDir()
				marker := filepath.Join(work, "module-imported")
				fixture := filepath.Join(work, "producer.json")
				output := filepath.Join(work, "redirected.json")
				const payload = "[{\"id\":\"synthetic\",\"bytes\":9007199254740993}]\n"
				if err := os.WriteFile(fixture, []byte(payload), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("SYNTHETIC_MODULE_PATH_MARKER", marker)
				t.Setenv("PSModuleAnalysisCachePath", filepath.Join(work, "module-analysis-cache"))
				shell := nativeShell{name: "powershell", executable: powershell,
					args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}}
				if supervised {
					shell.executable = helper
					shell.args = append([]string{"-test.run=^TestShellFileWindowsProcessHelper$", "--", powershell}, shell.args...)
				}
				if inherited {
					shell.name = "powershell-inherited-control"
				}
				producer := "& " + quote(helper) + " '-test.run=^TestFiniteJSONListShellProcess$' '--' 'produce' " + quote(fixture) + " 'observe-start'"
				script := "$ErrorActionPreference='Stop'; try { " + producer + " > " + quote(output) + "; exit $LASTEXITCODE } " +
					"catch { [Console]::Error.WriteLine('MODULE_PROBE_ERROR:' + $_.Exception.Message); exit 73 }"
				got := runNativeShell(t, shell, work, t.TempDir(), "http://127.0.0.1:1", script)
				data, markerErr := os.ReadFile(marker)
				if inherited {
					if got.code != 73 || got.stdout != "" || !strings.Contains(got.stderr, "SYNTHETIC_OUTFILE_SELECTED") ||
						markerErr != nil || string(data) != "synthetic-out-file-import" {
						t.Fatalf("inherited control did not reproduce owned module selection: result=%+v marker=%q error=%v", got, data, markerErr)
					}
					// Report whether redirection stopped the producer before its entry.
					_, enteredErr := os.Stat(fixture + ".entered")
					t.Logf("inherited control: module selected=true producer_entry_absent=%t", os.IsNotExist(enteredErr))
					return
				}
				if !os.IsNotExist(markerErr) || got.code != 0 || got.stdout != "" || got.stderr != "" {
					t.Fatalf("reset control failed: result=%+v marker=%q error=%v", got, data, markerErr)
				}
				redirected, encoding := readFiniteJSONListShellText(t, output)
				if string(redirected) != strings.ReplaceAll(payload, "\n", "\r\n") || encoding != "UTF-16LE" {
					t.Fatalf("native redirection changed: data=%q encoding=%s", redirected, encoding)
				}
				for _, suffix := range []string{".entered", ".finished"} {
					data, err := os.ReadFile(fixture + suffix)
					if err != nil || !strings.HasPrefix(string(data), "pid=") || !strings.Contains(string(data), " utc=") {
						t.Fatalf("producer checkpoint %s invalid: data=%q error=%v", suffix, data, err)
					}
				}
				t.Logf("reset control: module selected=false producer completed=true encoding=%s", encoding)
			})
		}
	}
	t.Run("pwsh-retains-explicit-path", func(t *testing.T) {
		pwsh := lookupShell(t, "pwsh")
		work := t.TempDir()
		shell := nativeShell{name: "pwsh", executable: pwsh,
			args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}}
		script := "$paths=$env:PSModulePath.Split([IO.Path]::PathSeparator); if ($paths -contains " + quote(moduleRoot) +
			") { [Console]::Out.WriteLine('PWSH_PATH_RETAINED'); exit 0 }; exit 74"
		got := runNativeShell(t, shell, work, t.TempDir(), "http://127.0.0.1:1", script)
		if got.code != 0 || got.stdout != "PWSH_PATH_RETAINED\r\n" || got.stderr != "" {
			t.Fatalf("PowerShell 7 path changed: %+v", got)
		}
	})
}
