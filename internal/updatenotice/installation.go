package updatenotice

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"syscall"
)

const (
	installationURL = "https://github.com/openai/openai-cli#installation"
	brewGuidance    = "brew upgrade --cask openai/tools/openai"
	goGuidance      = "go install github.com/openai/openai-cli/cmd/openai@latest"
	manualGuidance  = installationURL
)

func guidanceForRelease(command, latest string) string {
	// The verified unsuffixed Go module can install only v0/v1 releases.
	// Future major versions require a new module path that we cannot infer.
	if command == goGuidance && !strings.HasPrefix(latest, "0.") && !strings.HasPrefix(latest, "1.") {
		return manualGuidance
	}
	return command
}

func installationGuidance(current string) string {
	executable, err := os.Executable()
	if err != nil {
		return manualGuidance
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return manualGuidance
	}
	brew, err := exec.LookPath("brew")
	if err == nil && homebrewInstallation(executable, brew, current) {
		return brewGuidance
	}
	info, ok := debug.ReadBuildInfo()
	directory := goInstallDirectory()
	_, goErr := exec.LookPath("go")
	if ok && goErr == nil && directory != "" && goInstallation(executable, directory, current, info) {
		return goGuidance
	}
	return manualGuidance
}

func homebrewInstallation(executable, brew, current string) bool {
	// The release workflow publishes a cask containing one openai binary.
	if runtime.GOOS == "windows" || filepath.Base(executable) != "openai" {
		return false
	}
	versionDirectory := filepath.Dir(executable)
	if filepath.Base(versionDirectory) != current {
		return false
	}
	cask := filepath.Dir(versionDirectory)
	caskroom := filepath.Dir(cask)
	if filepath.Base(cask) != "openai" || filepath.Base(caskroom) != "Caskroom" {
		return false
	}
	prefix := filepath.Dir(caskroom)
	// Homebrew derives its prefix from the invoked directory before resolving
	// a final-file symlink. A PATH shim can therefore target another caskroom.
	selectedDirectory, err := filepath.EvalSymlinks(filepath.Dir(brew))
	if err != nil {
		return false
	}
	expectedDirectory, err := filepath.EvalSymlinks(filepath.Join(prefix, "bin"))
	if err != nil || selectedDirectory != expectedDirectory {
		return false
	}
	selected, err := filepath.EvalSymlinks(brew)
	if err != nil {
		return false
	}
	expected, err := filepath.EvalSymlinks(filepath.Join(prefix, "bin", "brew"))
	if err != nil || selected != expected {
		return false
	}
	data, err := readRegular(filepath.Join(cask, ".metadata", "INSTALL_RECEIPT.json"), 32*1024)
	if err != nil {
		return false
	}
	var receipt struct {
		Source struct {
			Tap     string `json:"tap"`
			Version string `json:"version"`
		} `json:"source"`
	}
	return json.Unmarshal(data, &receipt) == nil && receipt.Source.Tap == "openai/tools" && receipt.Source.Version == current
}

func goInstallation(executable, directory, current string, info *debug.BuildInfo) bool {
	if info.Path != "github.com/openai/openai-cli/cmd/openai" || info.Main.Path != "github.com/openai/openai-cli" || info.Main.Replace != nil {
		return false
	}
	version, ok := stableVersion(info.Main.Version)
	if !ok || version != current {
		return false
	}
	for _, setting := range info.Settings {
		if setting.Key == "vcs.modified" && setting.Value != "false" {
			return false
		}
	}
	name := "openai"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	destination := filepath.Join(directory, name)
	file, err := os.Lstat(destination)
	if err != nil || !file.Mode().IsRegular() {
		return false
	}
	// go install replaces its final destination. A symlink would update only
	// that link, leaving a directly invoked target executable unchanged.
	expected, err := filepath.EvalSymlinks(destination)
	return err == nil && expected == executable
}

// Only explicit GOBIN establishes the destination without running Go or
// approximating toolchain-specific GOROOT/go.env defaults.
func goInstallDirectory() string {
	settings := make(map[string]string)
	configuration := os.Getenv("GOENV")
	if configuration != "off" {
		if configuration == "" {
			directory, err := os.UserConfigDir()
			if err != nil {
				return ""
			}
			configuration = filepath.Join(directory, "go", "env")
		}
		data, err := readRegular(configuration, 16*1024)
		if err != nil && !os.IsNotExist(err) {
			return ""
		}
		for _, line := range strings.Split(string(data), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok && (key == "GOBIN" || key == "GOOS" || key == "GOARCH") {
				settings[key] = value
			}
		}
	}
	for _, key := range []string{"GOBIN", "GOOS", "GOARCH"} {
		if value := os.Getenv(key); value != "" {
			settings[key] = value
		}
	}
	// Go rejects cross-compilation with GOBIN. Do not infer the selected Go
	// toolchain's host from this CLI, which may itself run under emulation.
	if settings["GOOS"] != "" || settings["GOARCH"] != "" {
		return ""
	}
	destination := settings["GOBIN"]
	if filepath.IsAbs(destination) {
		return destination
	}
	return ""
}

func readRegular(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errCache
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		return nil, errCache
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errCache
	}
	return data, nil
}
