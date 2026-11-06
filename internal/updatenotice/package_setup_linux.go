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
		if enableInteractiveProfile(argv[2], argv[3], argv[4]) {
			fmt.Println("ready")
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
