package updatenotice

import (
	"errors"
	"os"
	"path/filepath"
)

func openManagedBin() (*os.Root, error) { return openWindowsManagedBin() }

func safeVerifierStore(root *os.Root) bool {
	opened, err := root.Open(".")
	if err != nil {
		return false
	}
	defer opened.Close()
	if err := checkWindowsUpdateHandle(opened, true, true); err != nil {
		return false
	}
	// The store lock is opened for writing by openCache before pin verification.
	err = inspectWindowsUpdatePath(filepath.Join(root.Name(), ".lock"), false, true)
	return err == nil || errors.Is(err, os.ErrNotExist)
}

func trustedSystemVerifier(path string) bool {
	if !localWindowsUpdatePath(path) {
		return false
	}
	cwd, err := os.Getwd()
	return err == nil && !sameWindowsName(filepath.Dir(path), cwd) && validateWindowsPath(path, false, false) == nil
}

// The PowerShell acquisition recipe keeps its authenticated gh.zip next to
// the starter. The caller checks both compiled hashes before executing gh.
const acquiredVerifierSetting = "OPENAI_CLI_WINDOWS_ACQUIRED_VERIFIER"

func acquiredVerifierArchive() (*os.File, error) {
	if os.Getenv(acquiredVerifierSetting) != "1" {
		return nil, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return openAcquiredVerifierArchive(filepath.Dir(executable))
}

func openAcquiredVerifierArchive(dir string) (*os.File, error) {
	if !localWindowsUpdatePath(dir) {
		return nil, errCache
	}
	if err := validateWindowsUpdatePath(dir, true); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "gh.zip")
	if err := validateWindowsUpdatePath(path, false); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !safeOpenedUpdateFile(file) {
		file.Close()
		return nil, errCache
	}
	return file, nil
}
