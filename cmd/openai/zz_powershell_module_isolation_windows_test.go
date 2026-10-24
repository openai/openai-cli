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

// External one-binary counterfactual. The original launcher stays unchanged.
// This exact prefix is bound to candidate.patch in the external source receipt.
const supervisedModuleIsolationPrefix = "$env:PSModulePath = [IO.Path]::Combine($PSHOME, 'Modules'); "

func TestMainNativeShellWindowsPowerShellModuleIsolationAB(t *testing.T) {
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	moduleRoot := t.TempDir()
	autoloadRoot := t.TempDir()
	for _, fixture := range []struct{ root, name, source string }{
		{moduleRoot, "Microsoft.PowerShell.Utility/Microsoft.PowerShell.Utility.psd1", `@{
RootModule = 'Microsoft.PowerShell.Utility.psm1'
ModuleVersion = '99.0.0'
GUID = '22222222-3333-4444-5555-666666666666'
FunctionsToExport = @('Out-File')
CmdletsToExport = @()
AliasesToExport = @()
VariablesToExport = @()
}`},
		{moduleRoot, "Microsoft.PowerShell.Utility/Microsoft.PowerShell.Utility.psm1", `
[IO.File]::WriteAllText($env:SYNTHETIC_MODULE_PATH_MARKER, 'utility-imported')
function Out-File {
    [CmdletBinding()]
    param([Parameter(ValueFromPipeline=$true)][object]$InputObject, [string]$FilePath, [switch]$Append)
    begin { throw 'SYNTHETIC_OUTFILE_SELECTED' }
}
`},
		{autoloadRoot, "OpenAICliIsolationFixture/OpenAICliIsolationFixture.psd1", `@{
RootModule = 'OpenAICliIsolationFixture.psm1'
ModuleVersion = '1.0.0'
GUID = '33333333-4444-5555-6666-777777777777'
FunctionsToExport = @('Invoke-CliIsolationFixture')
CmdletsToExport = @()
AliasesToExport = @()
VariablesToExport = @()
}`},
		{autoloadRoot, "OpenAICliIsolationFixture/OpenAICliIsolationFixture.psm1", `
[IO.File]::WriteAllText($env:SYNTHETIC_MODULE_PATH_MARKER, 'autoload-imported')
function Invoke-CliIsolationFixture {
    [Console]::Out.WriteLine('SYNTHETIC_AUTOLOAD_OK')
}
`},
	} {
		path := filepath.Join(fixture.root, filepath.FromSlash(fixture.name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fixture.source), 0600); err != nil {
			t.Fatal(err)
		}
		t.Logf("owned module=%s bytes=%d sha256=%x", fixture.name, len(fixture.source), sha256.Sum256([]byte(fixture.source)))
	}
	for _, variant := range []string{"baseline", "candidate"} {
		for _, control := range []struct{ name, shell string }{
			{"ambient_isolation", "powershell"},
			{"explicit_autoload", "powershell"},
			{"inherited_autoload", "pwsh"},
		} {
			t.Run(variant+"/"+control.shell+"/"+control.name, func(t *testing.T) {
				path, err := exec.LookPath(control.shell + ".exe")
				if err != nil {
					if slices.Contains(strings.Split(os.Getenv("OPENAI_CLI_REQUIRE_NATIVE_SHELLS"), ","), control.shell) {
						t.Fatalf("required shell %s is unavailable: %v", control.shell, err)
					}
					t.Skipf("%s is not installed", control.shell)
				}
				work := t.TempDir()
				marker := filepath.Join(work, "module-imported")
				t.Setenv("SYNTHETIC_MODULE_PATH_MARKER", marker)
				t.Setenv("PSModuleAnalysisCachePath", filepath.Join(work, "module-analysis-cache"))
				inheritedRoot := autoloadRoot
				if control.name == "ambient_isolation" {
					inheritedRoot = moduleRoot
				}
				t.Setenv("PSModulePath", inheritedRoot)
				shell := nativeShell{name: control.shell, executable: helper,
					args: []string{"-test.run=^TestShellFileWindowsProcessHelper$", "--", path,
						"-NoLogo", "-NoProfile", "-NonInteractive", "-Command"}}
				// Each failure emits only a fixed synthetic label.
				wrap := func(script string) string {
					return "$ErrorActionPreference='Stop'; try { " + script + " } catch { " +
						"if ($_.Exception.Message -like '*SYNTHETIC_OUTFILE_SELECTED*') { " +
						"[Console]::Error.WriteLine('SYNTHETIC_OUTFILE_SELECTED') } else { " +
						"[Console]::Error.WriteLine('SYNTHETIC_MODULE_CONTROL_FAILED') }; exit 73 }"
				}
				run := func(script string) mainDispatchResult {
					if variant == "candidate" && control.shell == "powershell" {
						script = supervisedModuleIsolationPrefix + script
					}
					return runNativeShell(t, shell, work, t.TempDir(), "http://127.0.0.1:1", script)
				}
				if control.name == "ambient_isolation" {
					fixture := filepath.Join(work, "producer.json")
					output := filepath.Join(work, "redirected.json")
					const payload = "[{\"id\":\"synthetic\",\"bytes\":9007199254740993}]\n"
					if err := os.WriteFile(fixture, []byte(payload), 0600); err != nil {
						t.Fatal(err)
					}
					producer := "& " + quote(helper) + " '-test.run=^TestFiniteJSONListShellProcess$' '--' 'produce' " + quote(fixture) + " 'observe-start'"
					got := run(wrap(producer + " > " + quote(output) + "; exit $LASTEXITCODE"))
					imported, markerErr := os.ReadFile(marker)
					if variant == "baseline" {
						if got.code != 73 || got.stdout != "" || got.stderr != "SYNTHETIC_OUTFILE_SELECTED\r\n" ||
							markerErr != nil || string(imported) != "utility-imported" {
							t.Fatalf("baseline selection not reproduced: exit=%d stdout_bytes=%d stderr_bytes=%d synthetic_error=%t exact_marker=%t",
								got.code, len(got.stdout), len(got.stderr), got.stderr == "SYNTHETIC_OUTFILE_SELECTED\r\n", markerErr == nil && string(imported) == "utility-imported")
						}
						t.Log("baseline ambient selection verified: exit=73 synthetic_error=true exact_marker=true")
						return
					}
					if got.code != 0 || got.stdout != "" || got.stderr != "" || !os.IsNotExist(markerErr) {
						t.Fatalf("ambient isolation failed: exit=%d stdout_bytes=%d stderr_bytes=%d synthetic_error=%t exact_marker=%t",
							got.code, len(got.stdout), len(got.stderr), got.stderr == "SYNTHETIC_OUTFILE_SELECTED\r\n", markerErr == nil && string(imported) == "utility-imported")
					}
					redirected, encoding := readFiniteJSONListShellText(t, output)
					if string(redirected) != strings.ReplaceAll(payload, "\n", "\r\n") || encoding != "UTF-16LE" {
						t.Fatalf("native redirection changed: bytes=%d encoding=%s", len(redirected), encoding)
					}
					for _, suffix := range []string{".entered", ".finished"} {
						data, err := os.ReadFile(fixture + suffix)
						if err != nil || !strings.HasPrefix(string(data), "pid=") || !strings.Contains(string(data), " utc=") {
							t.Fatalf("producer checkpoint %s invalid: bytes=%d error_type=%T", suffix, len(data), err)
						}
					}
					t.Log("ambient module excluded; native producer completed; encoding=UTF-16LE")
					return
				}
				script := "Invoke-CliIsolationFixture; exit 0"
				if control.name == "explicit_autoload" {
					// The candidate prefix executes before this explicit opt-in.
					script = "$env:PSModulePath = " + quote(autoloadRoot) + "+[IO.Path]::PathSeparator+$env:PSModulePath; " + script
				}
				got := run(wrap(script))
				imported, markerErr := os.ReadFile(marker)
				if got.code != 0 || got.stdout != "SYNTHETIC_AUTOLOAD_OK\r\n" || got.stderr != "" ||
					markerErr != nil || string(imported) != "autoload-imported" {
					t.Fatalf("native autoload failed: exit=%d stdout_bytes=%d stderr_bytes=%d exact_result=%t exact_marker=%t marker_error_type=%T",
						got.code, len(got.stdout), len(got.stderr), got.stdout == "SYNTHETIC_AUTOLOAD_OK\r\n", string(imported) == "autoload-imported", markerErr)
				}
				t.Log("owned module autoloaded; exact result and import marker verified; exit=0")
			})
		}
	}
}
