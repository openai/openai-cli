//go:build windows

package updatenotice

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/openai/openai-cli/internal/windowsfile"
	"golang.org/x/sys/windows"
)

const windowsWorkerImage = "worker.exe"

func enableZshPath(string, string) bool { return false }

func automaticPlatform() bool {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	return err == nil && !token.IsElevated() && !user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) &&
		!user.User.Sid.IsWellKnown(windows.WinLocalServiceSid) && !user.User.Sid.IsWellKnown(windows.WinNetworkServiceSid)
}

func managedExecutablePath(worker bool) (string, error) {
	path, err := windowsInstallPath()
	if err != nil {
		return "", err
	}
	expected := path
	if worker {
		expected = filepath.Join(filepath.Dir(path), ".update", windowsWorkerImage)
	}
	executable, err := os.Executable()
	if err != nil || !sameWindowsName(executable, expected) {
		return "", errCache
	}
	return path, nil
}

func authorizedUpdateImage(installation *updateInstallation, worker bool) bool {
	root, err := openWindowsManagedBin()
	if err != nil {
		return false
	}
	defer root.Close()
	current, err := root.Stat(".")
	if err != nil || !os.SameFile(current, installation.directory) || !safeOpenedUpdateFile(installation.file) {
		return false
	}
	enrolled, err := windowsArchiveEnrolled(filepath.Dir(installation.path))
	if err != nil || !enrolled {
		return false
	}
	if !worker {
		return true
	}
	// The detached helper has a distinct file identity, but must contain
	// exactly the bytes of the still-installed official executable.
	image, err := lockedWindowsImage(filepath.Join(installation.stateDirectory(), windowsWorkerImage))
	if err != nil {
		return false
	}
	defer image.Close()
	return sameWindowsImage(context.Background(), image, installation)
}

func openManagedUpdateCache(ctx context.Context, installation *updateInstallation) (*noticeCache, error) {
	return openPrivateWindowsUpdateCache(ctx, installation.stateDirectory())
}

func openPrivateWindowsUpdateCache(ctx context.Context, directory string) (*noticeCache, error) {
	created, err := windowsfile.CreatePrivateDirectory(directory)
	if err == nil {
		created.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if err := validateWindowsUpdatePath(directory, true); err != nil {
		return nil, err
	}
	// State and lock are opened for writing by the common cache. Reject
	// reparse points, additional hard links, and foreign DACLs before use.
	for _, name := range []string{".lock", stateName, "next", "next.tmp", "archive", "bundle", updateBundleName, windowsWorkerImage, "worker.tmp", "archive-install.tmp", "archive-install-marker.tmp"} {
		err := inspectWindowsUpdatePath(filepath.Join(directory, name), false, true)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return openCache(ctx, directory)
}

func startUpdateWorker(path, current string) {
	command := exec.Command(path, workerArgument, current)
	command.Env = append(updateEnvironment(), "OPENAI_CLI_UPDATE_WORKER=1")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if command.Start() == nil {
		go func() { _ = command.Wait() }()
	}
}

func isolateVerifier(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}

// A worker executing the public openai.exe would keep the destination mapped
// while trying to replace it. The initially detached process pins a private
// byte-for-byte copy, starts it, and exits. Foreground commands do no copying.
func relayUpdateWorker(ctx context.Context, current string) bool {
	self, err := os.Executable()
	path, pathErr := windowsInstallPath()
	if err != nil || pathErr != nil || !sameWindowsName(self, path) {
		return false
	}
	if _, enabled := updatePolicy(os.Getenv); !enabled {
		return true
	}
	installation, err := managedInstallation(current, false)
	if err != nil {
		return true
	}
	defer installation.close()
	cache, err := openManagedUpdateCache(ctx, installation)
	if err != nil {
		return true
	}
	pinned, err := pinWindowsWorker(ctx, installation, cache)
	cache.close()
	if err != nil {
		return true
	}
	defer pinned.Close()
	startUpdateWorker(filepath.Join(installation.stateDirectory(), windowsWorkerImage), current)
	return true
}

func lockedWindowsImage(path string) (*os.File, error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	// Hold through CreateProcess. A file cannot be rewritten, deleted or
	// replaced after we have verified its contents and before the loader opens it.
	handle, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if !safeOpenedUpdateFile(file) {
		file.Close()
		return nil, errCache
	}
	return file, nil
}

func sameWindowsImage(ctx context.Context, image *os.File, installation *updateInstallation) bool {
	candidate, err := fileDigest(ctx, image)
	if err != nil {
		return false
	}
	source, err := installation.digest(ctx)
	return err == nil && candidate == source
}

func pinWindowsWorker(ctx context.Context, installation *updateInstallation, cache *noticeCache) (*os.File, error) {
	path := filepath.Join(cache.directory, windowsWorkerImage)
	existing, err := lockedWindowsImage(path)
	if err == nil {
		if sameWindowsImage(ctx, existing, installation) {
			return existing, nil
		}
		existing.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := removeUpdateFile(cache, "worker.tmp"); err != nil {
		return nil, err
	}
	copyFile, err := windowsfile.CreatePrivate(cache.root, "worker.tmp")
	if err != nil {
		return nil, err
	}
	defer cache.root.Remove("worker.tmp")
	source := io.NewSectionReader(installation.file, 0, installation.identity.Size())
	_, err = io.Copy(copyFile, contextReader{ctx, source})
	err = errors.Join(err, copyFile.Sync(), copyFile.Close())
	if err != nil {
		return nil, err
	}
	if !cache.validLock() || !installation.unchanged() {
		return nil, errCache
	}
	if err := copyWindowsDownloadZone(installation.path, filepath.Join(cache.directory, "worker.tmp")); err != nil {
		return nil, err
	}
	if err = cache.root.Rename("worker.tmp", windowsWorkerImage); err != nil {
		return nil, err
	}
	image, err := lockedWindowsImage(path)
	if err != nil {
		return nil, err
	}
	if !sameWindowsImage(ctx, image, installation) {
		image.Close()
		return nil, errCache
	}
	return image, nil
}

func preservePreparedImageTrust(cache *noticeCache) error {
	return copyWindowsDownloadZone(filepath.Join(filepath.Dir(cache.directory), "openai.exe"), filepath.Join(cache.directory, "next.tmp"))
}

func sameUpdateDirectory(a, b string) bool { return sameWindowsName(a, b) }

func windowsImageBusy(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_USER_MAPPED_FILE)
}

func replaceUpdateImage(ctx context.Context, replace func() error) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := replace()
		if !windowsImageBusy(err) {
			return err
		}
		if !time.Now().Before(deadline) {
			return errors.Join(errBusyUpdateImage, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
