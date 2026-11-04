//go:build windows

package updatenotice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-cli/internal/windowsfile"
	"golang.org/x/sys/windows"
)

type windowsSetupMessage string

func (m windowsSetupMessage) Error() string { return string(m) }

const windowsEnrollmentMarker = "openai windows archive installer v1\n"
const windowsUnmanagedTarget = windowsSetupMessage("the installation directory already contains an unmanaged openai.exe; move that copy before retrying setup")

// windowsArchiveEnrolled recognizes only this installer's private, complete marker.
func windowsArchiveEnrolled(bin string) (bool, error) {
	path := filepath.Join(bin, ".archive-install")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err := validateWindowsUpdatePath(path, false); err != nil {
		return false, err
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	if !safeOpenedUpdateFile(f) {
		return false, errCache
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(len(windowsEnrollmentMarker)+1)))
	if err != nil || string(data) != windowsEnrollmentMarker {
		return false, windowsSetupMessage("the existing archive installation marker is unrecognized")
	}
	return true, nil
}

// InstallWindowsArchive enrolls the currently running official ZIP executable.
// Public diagnostics contain authored instructions, never filesystem or verifier output.
func InstallWindowsArchive(ctx context.Context) (string, error) {
	path, err := installWindowsArchive(ctx)
	if err != nil {
		var message windowsSetupMessage
		if errors.As(err, &message) {
			return "", message
		}
		return "", windowsSetupMessage("could not prepare the private OpenAI CLI installation; check your account's access and retry setup")
	}
	return path, nil
}

func installWindowsArchive(ctx context.Context) (string, error) {
	if !automaticPlatform() {
		return "", windowsSetupMessage("run setup in an ordinary terminal without administrator privileges")
	}
	target, err := windowsInstallPath()
	if err != nil {
		return "", err
	}
	localAppData, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil || !sameWindowsName(target, filepath.Join(localAppData, "OpenAI", "CLI", "bin", "openai.exe")) {
		return "", windowsSetupMessage("run setup with your account's normal LOCALAPPDATA setting")
	}
	if err := validateWindowsPath(localAppData, true, false); err != nil {
		return "", windowsSetupMessage("the account's local application directory cannot safely contain the installation")
	}
	source, err := os.Executable()
	version, stable := stableVersion(releaseVersion)
	if err != nil || !stable || version != releaseVersion || !validCommit(releaseCommit) {
		return "", windowsSetupMessage("run setup from an extracted official OpenAI CLI Windows ZIP")
	}
	if err := validateWindowsPath(source, false, false); err != nil {
		return "", windowsSetupMessage("extract the official ZIP into a directory only your account can modify")
	}
	executable, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer executable.Close()
	if checkWindowsUpdateHandle(executable, false, false) != nil || !binaryIdentity(executable, releaseVersion, releaseCommit) {
		return "", windowsSetupMessage("setup requires the release executable from the official Windows ZIP")
	}
	ctx, cancel := context.WithTimeout(ctx, workerTimeout+10*time.Second)
	defer cancel()
	bin := filepath.Dir(target)
	cacheDir := filepath.Join(bin, ".update")
	for _, dir := range []string{filepath.Dir(filepath.Dir(bin)), filepath.Dir(bin), bin} {
		created, err := windowsfile.CreatePrivateDirectory(dir)
		if err == nil {
			created.Close()
		} else if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("create private installation directory: %w", err)
		}
		// Other applications can share the company directory. Read-only grants
		// there cannot redirect the protected CLI directories below it.
		if err := validateWindowsPath(dir, true, dir != filepath.Dir(filepath.Dir(bin))); err != nil {
			return "", err
		}
	}
	// A foreign executable must not start a verifier download. Check again
	// under the updater lock before publishing any installation.
	enrolled, err := windowsArchiveEnrolled(bin)
	if err != nil {
		return "", err
	}
	if !enrolled {
		if _, err := os.Lstat(target); err == nil {
			return "", windowsUnmanagedTarget
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	// Run the bootstrap through the same release image. Keep SDK credentials
	// and injection settings out of the verifier's environment.
	probe := exec.CommandContext(ctx, source, "__openai_setup_update_verifier")
	probe.Env = append(updateEnvironment(), "OPENAI_CLI_ARCHIVE_INSTALL=1")
	isolateVerifier(probe)
	// The helper emits fixed progress and recovery messages on stderr.
	// Keep the installed path as the only successful stdout record.
	probe.Stderr = os.Stderr
	result, err := probe.Output()
	if err != nil || string(result) != "ready\n" {
		return "", windowsSetupMessage("could not prepare the update verifier; check network access and retry setup")
	}
	cache, err := openInstallerCache(ctx, true, func() (*noticeCache, error) { return openPrivateWindowsUpdateCache(ctx, cacheDir) })
	if err != nil {
		return "", windowsSetupMessage("cannot lock the private installation directory; retry setup after other CLI activity finishes")
	}
	defer cache.close()
	enrolled, err = windowsArchiveEnrolled(bin)
	if err != nil {
		return "", err
	}
	sourceHash, err := fileDigest(ctx, executable)
	if err != nil {
		return "", err
	}
	state, err := loadUpdateState(cache, time.Now())
	if err != nil {
		return "", err
	}
	installed, err := existingWindowsArchive(ctx, target, enrolled, sourceHash, state)
	if err != nil {
		return "", err
	}
	if installed.identity == nil || newerVersion(releaseVersion, installed.version) {
		if err := copyWindowsArchive(ctx, cache, executable, source, sourceHash, target, enrolled, installed.identity); err != nil {
			return "", err
		}
		installed = windowsArchiveImage{version: releaseVersion, commit: releaseCommit, digest: sourceHash}
	}
	if state.AppliedHash != installed.digest || state.AppliedCommit != installed.commit || state.Applied != installed.version {
		// The rename publishes the image and its setup receipt together. A
		// retry repairs this shared receipt after an intervening process exit.
		receipt := updateState{Schema: 1, Applied: installed.version, AppliedCommit: installed.commit, AppliedHash: installed.digest, Announced: true}
		if err := cache.saveState(receipt); err != nil {
			return "", err
		}
	}
	conflict, err := registerWindowsUserPath(bin)
	if err != nil {
		var message windowsSetupMessage
		if errors.As(err, &message) {
			return "", message
		}
		return "", windowsSetupMessage(`could not update the user PATH. Run the managed CLI with & (Join-Path $env:LOCALAPPDATA 'OpenAI\CLI\bin\openai.exe')`)
	}
	if conflict {
		fmt.Fprintln(os.Stderr, `New terminals select the machine PATH copy first. To select this installation in a new PowerShell, run: $env:Path = "$env:LOCALAPPDATA\OpenAI\CLI\bin;$env:Path"`)
	}
	return target, nil
}

type windowsArchiveImage struct {
	identity                os.FileInfo
	version, commit, digest string
}

// A stream follows the executable through the same atomic rename. This lets
// setup recover when the process exits before the shared updater state lands.
const windowsArchiveReceiptStream = ":OpenAI.CLI.InstallReceipt"

func existingWindowsArchive(ctx context.Context, target string, enrolled bool, sourceHash string, state updateState) (windowsArchiveImage, error) {
	var image windowsArchiveImage
	name, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return image, nil
	}
	if err != nil {
		return image, err
	}
	if !enrolled {
		return image, windowsUnmanagedTarget
	}
	if err := validateWindowsUpdatePath(target, false); err != nil {
		return image, err
	}
	f, err := os.Open(target)
	if err != nil {
		return image, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !sameUpdateFile(name, actual) || !safeOpenedUpdateFile(f) {
		return image, errCache
	}
	hash, err := fileDigest(ctx, f)
	if err != nil {
		return image, err
	}
	image = windowsArchiveImage{identity: actual, digest: hash}
	switch {
	case hash == sourceHash:
		image.version, image.commit = releaseVersion, releaseCommit
	case hash == state.AppliedHash && binaryIdentity(f, state.Applied, state.AppliedCommit):
		image.version, image.commit = state.Applied, state.AppliedCommit
	default:
		receipt, err := os.Open(target + windowsArchiveReceiptStream)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(receipt, 4097))
			receipt.Close()
			fields := strings.Split(string(data), "\n")
			if readErr == nil && len(data) <= 4096 && len(fields) == 4 && fields[3] == "" && fields[2] == hash && validCommit(fields[1]) {
				version, stable := stableVersion(fields[0])
				if stable && version == fields[0] && binaryIdentity(f, version, fields[1]) {
					image.version, image.commit = version, fields[1]
					return image, nil
				}
			}
		}
		return image, windowsSetupMessage("the managed executable does not match a verified installation or update receipt")
	}
	return image, nil
}

// copyWindowsArchive stages all bytes and metadata before either destination
// becomes visible. A replacement requires the previously verified file identity.
func copyWindowsArchive(ctx context.Context, cache *noticeCache, source *os.File, sourcePath, sourceHash, target string, enrolled bool, previous os.FileInfo) error {
	const staged = "archive-install.tmp"
	if err := removeUpdateFile(cache, staged); err != nil {
		return err
	}
	stage, err := windowsfile.CreatePrivate(cache.root, staged)
	if err != nil {
		return err
	}
	defer cache.root.Remove(staged)
	stagePath := filepath.Join(cache.directory, staged)
	_, copyErr := io.Copy(stage, contextReader{ctx, source})
	if copyErr == nil {
		copyErr = stage.Sync()
	}
	closeErr := stage.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := copyWindowsDownloadZone(sourcePath, stagePath); err != nil {
		return fmt.Errorf("preserve the Windows download security zone: %w", err)
	}
	if previous != nil {
		if _, err := os.Stat(stagePath + ":Zone.Identifier"); errors.Is(err, os.ErrNotExist) {
			if err := copyWindowsDownloadZone(target, stagePath); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	receipt, err := os.OpenFile(stagePath+windowsArchiveReceiptStream, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(receipt, "%s\n%s\n%s\n", releaseVersion, releaseCommit, sourceHash)
	if err = errors.Join(err, receipt.Sync(), receipt.Close()); err != nil {
		return err
	}
	stagedFile, err := os.Open(stagePath)
	if err != nil {
		return err
	}
	hash, err := fileDigest(ctx, stagedFile)
	stagedFile.Close()
	if err != nil || hash != sourceHash || !cache.validLock() {
		return errors.New("the staged executable changed during setup")
	}
	if !enrolled {
		const stagedMarker = "archive-install-marker.tmp"
		if err := removeUpdateFile(cache, stagedMarker); err != nil {
			return err
		}
		marker, err := windowsfile.CreatePrivate(cache.root, stagedMarker)
		if err != nil {
			return err
		}
		defer cache.root.Remove(stagedMarker)
		_, err = io.WriteString(marker, windowsEnrollmentMarker)
		if err == nil {
			err = marker.Sync()
		}
		closeErr := marker.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if err := moveWindowsArchive(filepath.Join(cache.directory, stagedMarker), filepath.Join(filepath.Dir(target), ".archive-install"), 0); err != nil {
			return err
		}
	}
	if previous == nil {
		return moveWindowsArchive(stagePath, target, 0)
	}
	err = replaceUpdateImage(ctx, func() error {
		current, err := os.Lstat(target)
		if err != nil || !sameUpdateFile(previous, current) || !cache.validLock() {
			return errCache
		}
		return moveWindowsArchive(stagePath, target, windows.MOVEFILE_REPLACE_EXISTING)
	})
	if windowsImageBusy(err) {
		return windowsSetupMessage("the installed CLI is in use. Close the running CLI, then retry setup")
	}
	return err
}

func moveWindowsArchive(from, to string, flags uint32) error {
	src, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	dst, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(src, dst, flags|windows.MOVEFILE_WRITE_THROUGH)
}

// A download-zone stream contains security metadata. Limit its copy even if
// another process grows the stream after it opens. Refuse the entire staged
// executable on failure, preserving the original stream and security zone.
const maxWindowsZoneBytes = 1 << 20

func copyWindowsDownloadZone(source, target string) error {
	const zone = ":Zone.Identifier"
	in, err := os.Open(source + zone)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target+zone, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, maxWindowsZoneBytes+1))
	if err == nil && n > maxWindowsZoneBytes {
		err = errors.New("oversized Windows download security zone")
	}
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(target + zone)
	}
	return err
}
