package updatenotice

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"testing"
)

func fixtureFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestHomebrewInstallationRequiresReceiptAndSelectedPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew cask paths are Unix-specific")
	}
	prefix := filepath.Join(t.TempDir(), "brew prefix")
	executable := filepath.Join(prefix, "Caskroom", "openai", "1.2.3", "openai")
	brew := filepath.Join(prefix, "bin", "brew")
	receipt := filepath.Join(prefix, "Caskroom", "openai", ".metadata", "INSTALL_RECEIPT.json")
	fixtureFile(t, executable, "synthetic executable")
	fixtureFile(t, brew, "synthetic brew")
	valid := `{"source":{"tap":"openai/tools","version":"1.2.3"}}`
	fixtureFile(t, receipt, valid)
	if !homebrewInstallation(executable, brew, "1.2.3") {
		t.Fatal("official cask was not recognized")
	}
	otherBrew := filepath.Join(t.TempDir(), "bin", "brew")
	fixtureFile(t, otherBrew, "other brew")
	if homebrewInstallation(executable, otherBrew, "1.2.3") {
		t.Fatal("other Homebrew prefix was accepted")
	}
	for name, content := range map[string]string{
		"wrong tap":      `{"source":{"tap":"other/tools","version":"1.2.3"}}`,
		"wrong version":  `{"source":{"tap":"openai/tools","version":"1.2.2"}}`,
		"missing source": `{}`, "malformed": `{`,
	} {
		t.Run(name, func(t *testing.T) {
			fixtureFile(t, receipt, content)
			if homebrewInstallation(executable, brew, "1.2.3") {
				t.Fatal("unverified cask was accepted")
			}
		})
	}
	fixtureFile(t, receipt, valid)
	if homebrewInstallation(executable, brew, "1.2.4") {
		t.Fatal("mismatched executable version was accepted")
	}
	if homebrewInstallation(filepath.Join(prefix, "Cellar", "openai", "1.2.3", "bin", "openai"), brew, "1.2.3") {
		t.Fatal("unverified formula was accepted")
	}
}

func TestGoInstallationRequiresVersionedModuleAndLocation(t *testing.T) {
	directory := t.TempDir()
	name := "openai"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	executable := filepath.Join(directory, name)
	fixtureFile(t, executable, "synthetic executable")
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	base := debug.BuildInfo{Path: "github.com/openai/openai-cli/cmd/openai", Main: debug.Module{Path: "github.com/openai/openai-cli", Version: "v1.2.3"}}
	if !goInstallation(resolved, directory, "1.2.3", &base) {
		t.Fatal("Go installation was not recognized")
	}
	for name, change := range map[string]func(*debug.BuildInfo){
		"development":   func(info *debug.BuildInfo) { info.Main.Version = "(devel)" },
		"prerelease":    func(info *debug.BuildInfo) { info.Main.Version = "v1.2.3-beta" },
		"other module":  func(info *debug.BuildInfo) { info.Main.Path = "example.com/cli" },
		"other command": func(info *debug.BuildInfo) { info.Path = "example.com/openai" },
		"replacement":   func(info *debug.BuildInfo) { info.Main.Replace = &debug.Module{Path: "./local"} },
		"modified": func(info *debug.BuildInfo) {
			info.Settings = []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			info := base
			change(&info)
			if goInstallation(resolved, directory, "1.2.3", &info) {
				t.Fatal("custom build was accepted")
			}
		})
	}
	if goInstallation(resolved, t.TempDir(), "1.2.3", &base) {
		t.Fatal("copied executable was accepted")
	}
	if goInstallation(resolved, directory, "1.2.4", &base) {
		t.Fatal("mismatched build version was accepted")
	}
}

func TestHomebrewInstallationPreservesInvocationPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew cask paths are Unix-specific")
	}
	root := t.TempDir()
	prefix := filepath.Join(root, "brew prefix")
	executable := filepath.Join(prefix, "Caskroom", "openai", "1.2.3", "openai")
	brew := filepath.Join(prefix, "bin", "brew")
	fixtureFile(t, executable, "synthetic executable")
	fixtureFile(t, brew, "synthetic brew")
	fixtureFile(t, filepath.Join(prefix, "Caskroom", "openai", ".metadata", "INSTALL_RECEIPT.json"), `{"source":{"tap":"openai/tools","version":"1.2.3"}}`)
	binAlias := filepath.Join(root, "bin alias")
	if err := os.Symlink(filepath.Dir(brew), binAlias); err != nil {
		t.Fatal(err)
	}
	if !homebrewInstallation(executable, filepath.Join(binAlias, "brew"), "1.2.3") {
		t.Fatal("alias of the same invoked directory was rejected")
	}
	shim := filepath.Join(root, "shim", "bin", "brew")
	if err := os.MkdirAll(filepath.Dir(shim), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(brew, shim); err != nil {
		t.Fatal(err)
	}
	if homebrewInstallation(executable, shim, "1.2.3") {
		t.Fatal("final-file symlink with a different invoked prefix was accepted")
	}
}

func TestGoInstallDirectoryRejectsCrossTargetConfiguration(t *testing.T) {
	otherOS, otherArch := "windows", "amd64"
	if runtime.GOOS == otherOS {
		otherOS = "linux"
	}
	if runtime.GOARCH == otherArch {
		otherArch = "arm64"
	}
	for _, tc := range []struct {
		name, storedOS, storedArch, processOS, processArch string
		processBin, disabled, accepted                     bool
	}{
		{name: "native defaults", accepted: true},
		{name: "process OS", processOS: otherOS},
		{name: "process architecture", processArch: otherArch},
		{name: "persisted OS with process destination", storedOS: otherOS, processBin: true},
		{name: "persisted architecture with process destination", storedArch: otherArch, processBin: true},
		{name: "native process target remains custom", storedOS: otherOS, storedArch: otherArch, processOS: runtime.GOOS, processArch: runtime.GOARCH},
		{name: "disabled persisted target", storedOS: otherOS, storedArch: otherArch, processBin: true, disabled: true, accepted: true},
		{name: "disabled configuration retains process target", processOS: otherOS, processBin: true, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "go bin")
			configuration := filepath.Join(root, "go-env")
			fixtureFile(t, configuration, "GOBIN="+directory+"\nGOOS="+tc.storedOS+"\nGOARCH="+tc.storedArch+"\n")
			t.Setenv("GOENV", configuration)
			t.Setenv("GOBIN", "")
			t.Setenv("GOOS", tc.processOS)
			t.Setenv("GOARCH", tc.processArch)
			if tc.processBin {
				t.Setenv("GOBIN", directory)
			}
			if tc.disabled {
				t.Setenv("GOENV", "off")
			}
			want := ""
			if tc.accepted {
				want = directory
			}
			if got := goInstallDirectory(); got != want {
				t.Fatalf("installation destination = %q; want %q", got, want)
			}
		})
	}
}

func TestGoInstallDirectoryHonorsPersistedAndProcessOverrides(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "go-env")
	storedBin := filepath.Join(root, "stored bin")
	storedPath := filepath.Join(root, "stored path")
	processBin := filepath.Join(root, "process bin")
	t.Setenv("GOENV", config)
	t.Setenv("GOBIN", "")
	t.Setenv("GOOS", "")
	t.Setenv("GOARCH", "")
	t.Setenv("GOPATH", "")
	t.Setenv("HOME", root)
	t.Setenv("USERPROFILE", root)
	fixtureFile(t, config, "GOBIN="+storedBin+"\nGOPATH="+storedPath+"\n")
	if got := goInstallDirectory(); got != storedBin {
		t.Fatalf("persisted GOBIN = %q", got)
	}
	t.Setenv("GOBIN", processBin)
	if got := goInstallDirectory(); got != processBin {
		t.Fatalf("process GOBIN = %q", got)
	}
	t.Setenv("GOBIN", "")
	fixtureFile(t, config, "GOPATH="+storedPath+string(os.PathListSeparator)+filepath.Join(root, "second")+"\n")
	if got := goInstallDirectory(); got != "" {
		t.Fatalf("GOPATH-only destination = %q", got)
	}
	t.Setenv("GOENV", "off")
	if got := goInstallDirectory(); got != "" {
		t.Fatalf("unproven default destination = %q", got)
	}
	t.Setenv("GOBIN", "relative")
	if got := goInstallDirectory(); got != "" {
		t.Fatalf("relative path = %q", got)
	}
}

func TestGoInstallationRejectsFinalSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks can require Windows privileges")
	}
	root := t.TempDir()
	executable := filepath.Join(root, "custom", "openai")
	directory := filepath.Join(root, "go-bin")
	fixtureFile(t, executable, "synthetic executable")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(executable, filepath.Join(directory, "openai")); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	info := debug.BuildInfo{Path: "github.com/openai/openai-cli/cmd/openai", Main: debug.Module{Path: "github.com/openai/openai-cli", Version: "v1.2.3"}}
	if goInstallation(resolved, directory, "1.2.3", &info) {
		t.Fatal("final destination symlink was accepted")
	}
	if err := os.Remove(filepath.Join(directory, "openai")); err != nil {
		t.Fatal(err)
	}
	fixtureFile(t, filepath.Join(directory, "openai"), "synthetic installed executable")
	parentLink := filepath.Join(root, "go-parent-link")
	if err := os.Symlink(directory, parentLink); err != nil {
		t.Fatal(err)
	}
	resolved, err = filepath.EvalSymlinks(filepath.Join(directory, "openai"))
	if err != nil {
		t.Fatal(err)
	}
	if !goInstallation(resolved, parentLink, "1.2.3", &info) {
		t.Fatal("regular destination below a parent symlink was rejected")
	}
}
