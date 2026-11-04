//go:build !windows

package updatenotice

import (
	"context"
	"os"
	"path/filepath"
)

func managedExecutablePath(_ bool) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return "", err
	}
	path := filepath.Join(home, ".openai", "bin", "openai")
	executable, err := os.Executable()
	if err != nil || filepath.Clean(executable) != path {
		return "", errCache
	}
	return path, nil
}

func openManagedUpdateCache(ctx context.Context, installation *updateInstallation) (*noticeCache, error) {
	return openCache(ctx, installation.stateDirectory())
}

func authorizedUpdateImage(*updateInstallation, bool) bool             { return true }
func safeOpenedUpdateFile(*os.File) bool                               { return true }
func relayUpdateWorker(context.Context, string) bool                   { return false }
func replaceUpdateImage(_ context.Context, replace func() error) error { return replace() }

func markDownloadedExecutable(string) error { return nil }

func safeVerifierStore(*os.Root) bool { return true }

func sameUpdateDirectory(a, b string) bool { return a == b }

func acquiredVerifierArchive() (*os.File, error) { return nil, nil }

func openActivationImage(root *os.Root) (*os.File, error) { return openUpdateFile(root, "next") }

func pinActivationImage(_ context.Context, _ *os.Root, file *os.File, before os.FileInfo, _ string) (*os.File, os.FileInfo, error) {
	return file, before, nil
}

func renameActivationImage(root *os.Root, _ *os.File, target string) error {
	return root.Rename(".update/next", target)
}
