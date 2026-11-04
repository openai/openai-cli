package updatenotice

import (
	"fmt"
	"os"
)

func runLinuxPackageHelper(argv []string) bool {
	if len(argv) < 2 || os.Getenv("OPENAI_CLI_ARCHIVE_INSTALL") != "1" {
		return false
	}
	switch argv[1] {
	case "__openai_linux_package_starter":
		if len(argv) == 3 && validPackageStarter(argv[2]) {
			fmt.Println("ready")
		}
	case "__openai_linux_package_profile":
		if len(argv) != 5 {
			return true
		}
		switch argv[4] {
		case ".zshrc", ".bashrc", ".bash_profile", ".bash_login", ".profile":
			// A Bash login profile can also source .bashrc. Select the private
			// copy once and leave noninteractive login scripts alone.
			const line = `case $- in *i*) case "$PATH" in "$HOME/.openai/bin"|"$HOME/.openai/bin":*) ;; *) export PATH="$HOME/.openai/bin:$PATH" ;; esac ;; esac`
			if appendProfilePath(argv[2], argv[3], argv[4], line) {
				fmt.Println("ready")
			}
		}
	default:
		return false
	}
	return true
}

// The package and installed archive share the release-stamped executable.
func validPackageStarter(current string) bool {
	version, valid := stableVersion(current)
	if !automaticPlatform() || !valid || current != version || version != releaseVersion || !validCommit(releaseCommit) {
		return false
	}
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	file, err := os.Open(executable)
	if err != nil {
		return false
	}
	defer file.Close()
	return binaryIdentity(file, version, releaseCommit)
}
