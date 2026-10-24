//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// This separate process describes ambient module selection after tracing ends.
func captureOwnedWaitIdentity(t *testing.T, shell nativeShell, directory, scriptPath string) {
	t.Helper()
	source, err := readOwnedWaitBounded(scriptPath, 16384)
	if err != nil {
		t.Errorf("post-trace identity source unavailable: error_type=%T", err)
		return
	}
	pwsh, err := exec.LookPath("pwsh.exe")
	if err != nil {
		t.Errorf("post-trace identity shell unavailable: error_type=%T", err)
		return
	}
	script := "& {\n" + string(source) + "\n} -PwshHome '" + strings.ReplaceAll(filepath.Dir(pwsh), "'", "''") + "'"
	got := runNativeShell(t, shell, t.TempDir(), t.TempDir(), "http://127.0.0.1:1", script)
	data := []byte(strings.TrimSpace(got.stdout))
	if got.stderr != "" || !validOwnedWaitIdentity(data, got.code) {
		t.Errorf("post-trace identity rejected: exit=%d stdout_bytes=%d stderr_bytes=%d", got.code, len(got.stdout), len(got.stderr))
		return
	}
	if err := os.WriteFile(filepath.Join(directory, "wait-module-identity.json"), data, 0600); err != nil {
		t.Errorf("post-trace identity write failed: error_type=%T", err)
	}
}

func validOwnedWaitIdentity(data []byte, exit int) bool {
	if len(data) == 0 || len(data) > 4096 || !slices.Contains([]int{0, 75, 76, 77}, exit) {
		return false
	}
	fields := json.NewDecoder(bytes.NewReader(data))
	opening, err := fields.Token()
	if err != nil || opening != json.Delim('{') {
		return false
	}
	seen := make(map[string]bool, 28)
	allowed := strings.Fields("schema scope status error_type engine_version edition process64 host host_pid host_role host_sha256 host_bytes engine_assembly_version engine_role engine_sha256 engine_bytes utility_loaded_before_lookup command module module_version manifest_role manifest_sha256 manifest_bytes implementation assembly_version assembly_role assembly_sha256 assembly_bytes")
	for fields.More() {
		key, err := fields.Token()
		name, valid := key.(string)
		if err != nil || !valid || seen[name] || !slices.Contains(allowed, name) {
			return false
		}
		var value json.RawMessage
		if fields.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return false
		}
		seen[name] = true
	}
	if len(seen) != 28 {
		return false
	}
	var record struct {
		Schema                int    `json:"schema"`
		Scope                 string `json:"scope"`
		Status                string `json:"status"`
		Error                 string `json:"error_type"`
		EngineVersion         string `json:"engine_version"`
		Edition               string `json:"edition"`
		Process64             bool   `json:"process64"`
		Host                  string `json:"host"`
		PID                   int    `json:"host_pid"`
		HostRole              string `json:"host_role"`
		HostHash              string `json:"host_sha256"`
		HostBytes             int64  `json:"host_bytes"`
		EngineAssemblyVersion string `json:"engine_assembly_version"`
		EngineRole            string `json:"engine_role"`
		EngineHash            string `json:"engine_sha256"`
		EngineBytes           int64  `json:"engine_bytes"`
		Loaded                int    `json:"utility_loaded_before_lookup"`
		Command               string `json:"command"`
		Module                string `json:"module"`
		ModuleVersion         string `json:"module_version"`
		ManifestRole          string `json:"manifest_role"`
		ManifestHash          string `json:"manifest_sha256"`
		ManifestBytes         int64  `json:"manifest_bytes"`
		Implementation        string `json:"implementation"`
		AssemblyVersion       string `json:"assembly_version"`
		AssemblyRole          string `json:"assembly_role"`
		AssemblyHash          string `json:"assembly_sha256"`
		AssemblyBytes         int64  `json:"assembly_bytes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&record) != nil {
		return false
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF || record.Schema != 1 || record.Scope != "separate_process_post_trace" {
		return false
	}
	expected := map[int][]string{0: {"observed"}, 75: {"failed", "implementation_unavailable"}, 76: {"unexpected_cmdlet"}, 77: {"unexpected_assembly"}}
	if !slices.Contains(expected[exit], record.Status) || !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,80}$`).MatchString(record.Error) {
		return false
	}
	if record.Command != "Out-File" || record.Module != "Microsoft.PowerShell.Utility" || record.PID < 0 || record.Loaded < 0 || record.Loaded > 128 {
		return false
	}
	if !slices.Contains([]string{"unavailable", "powershell.exe", "pwsh.exe"}, record.Host) || !slices.Contains([]string{"unavailable", "Desktop", "Core"}, record.Edition) {
		return false
	}
	if !slices.Contains([]string{"unavailable", "Microsoft.PowerShell.Commands.Utility"}, record.Implementation) {
		return false
	}
	version := regexp.MustCompile(`^(unavailable|[0-9]{1,5}(\.[0-9]{1,10}){1,3}([-+][A-Za-z0-9.]{1,32})?)$`)
	for _, value := range []string{record.EngineVersion, record.EngineAssemblyVersion, record.ModuleVersion, record.AssemblyVersion} {
		if !version.MatchString(value) {
			return false
		}
	}
	for _, role := range []string{record.HostRole, record.EngineRole, record.ManifestRole, record.AssemblyRole} {
		if !slices.Contains([]string{"unavailable", "unexpected_name", "engine_modules", "engine_home", "pwsh_modules", "pwsh_home", "windows_gac", "other"}, role) {
			return false
		}
	}
	hash := regexp.MustCompile(`^(unavailable|[a-f0-9]{64})$`)
	for _, value := range []string{record.HostHash, record.EngineHash, record.ManifestHash, record.AssemblyHash} {
		if !hash.MatchString(value) {
			return false
		}
	}
	for _, count := range []int64{record.HostBytes, record.EngineBytes, record.ManifestBytes, record.AssemblyBytes} {
		if count < 0 || count > 64<<20 {
			return false
		}
	}
	return exit != 0 || record.PID > 0 && record.Host == "powershell.exe" && record.Implementation == "Microsoft.PowerShell.Commands.Utility"
}
