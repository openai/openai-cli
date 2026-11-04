// Package installation can opt into a private verifier without changing the user's gh.
package updatenotice

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const verifierVersion = "2.103.3"
const verifierDirectory = ".update-verifier"

type verifierPin struct {
	archive, binary string
}

// Each GitHub CLI release archive has an upstream attestation. Pin both the
// distributed archive and the one executable selected from it.
var verifierPins = map[string]verifierPin{
	"linux_386":     {"fe105cd3cb52cde68623778f67a438d95a0b66b9372f1c0b707c07a0f045f71d", "a9eab42ca10c0e8dacbe3bf85c889eb1f9e8b212305cb075eb37f9b40d56397f"},
	"linux_amd64":   {"4c806978ffce1cec70853302a56d70ac6da4f19bae7adca4e1a8ab00633dd91d", "b768184570b12536be4ac1528893f6340d1c6a7b40b56ad45bca9cd0e87ad34d"},
	"linux_arm64":   {"ee32df2323c8847f5fac6cb6b4b69ca75828e9329a8f22f677021f7b3306ce63", "a82a52e1bbe05bcd485f8cbbd5aa67e11a76fce6bdad394a2d179e1c24cfe367"},
	"linux_armv6":   {"57f56848b069cad21f4fa72f2cc38c2ac01f5f69f443159213a42183a73c89ea", "09eb91328a96d314b67041af4bc5da63ab1a3138fdfc3a6e6cdff6dc092ed928"},
	"windows_386":   {"e18001aaa03da36635626b3557947a886704d67f532292577c5945ad0535f6eb", "158ba584f8b8296858cea40f8527a46736453aac9fa6befbdffd2717d7ccb67f"},
	"windows_amd64": {"98d0cfa794721665d7973dd728c78aff7bd836d58f10f6d7a03568eccb0a22e0", "d303d8ac3f5e9438bf7c977b64dd6425513eae46218f8881d30694f28c1d0dfd"},
	"windows_arm64": {"197211c98687d7770ac331fe8a3b6c198c389108c1536cdbedf659fe4703e484", "c387d066328aadb51a8c385f7d361995589904c4fcec1b412957b033e97b3dad"},
}

func verifierPlatform() string {
	arch := runtime.GOARCH
	if arch == "arm" {
		arch = "armv6"
	}
	return runtime.GOOS + "_" + arch
}

func verifierEnrolled(bin string) bool {
	if _, supported := verifierPins[verifierPlatform()]; !supported {
		return false
	}
	info, err := os.Lstat(filepath.Join(bin, verifierDirectory))
	return err == nil && info.IsDir()
}

func openVerifierStore(enroll bool) (*os.Root, error) {
	bin, err := openVerifierBin()
	if err != nil {
		return nil, err
	}
	defer bin.Close()
	if enroll {
		if err := bin.Mkdir(verifierDirectory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	info, err := bin.Lstat(verifierDirectory)
	if err != nil || !info.IsDir() || !privateMode(info) || !ownedUpdatePath(info) {
		return nil, errCache
	}
	root, err := bin.OpenRoot(verifierDirectory)
	if err != nil {
		return nil, err
	}
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(info, actual) {
		root.Close()
		return nil, errCache
	}
	return root, nil
}

// Only the explicit installer creates the directory. Its presence permits an
// enrolled worker to restore a missing verifier or provision a later reviewed pin.
func setupUpdateVerifier(ctx context.Context) error {
	if !automaticPlatform() || !validCommit(releaseCommit) {
		return errUpdate
	}
	root, err := openVerifierStore(true)
	if err != nil {
		return err
	}
	defer root.Close()
	client := updateClient()
	defer client.CloseIdleConnections()
	system, _ := exec.LookPath("gh")
	d := &releaseDelivery{client: client, verifier: system}
	return d.selectVerifier(ctx, root.Name(), root, true)
}

func (d *releaseDelivery) selectVerifier(ctx context.Context, work string, root *os.Root, showProgress bool) error {
	if d.verifier != "" && d.checkVerifierHelp(ctx, work) == nil {
		return nil
	}
	path, err := d.privateVerifier(ctx, root, showProgress)
	if err != nil {
		return err
	}
	d.verifier = path
	return d.checkVerifierHelp(ctx, work)
}

func privateVerifierAsset() (string, verifierPin) {
	name := "gh-" + verifierVersion
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name, verifierPins[verifierPlatform()]
}

func existingPrivateVerifier(ctx context.Context, root *os.Root, name string, pin verifierPin) (string, error) {
	if _, err := root.Lstat(name); err != nil {
		return "", err
	}
	file, err := openUpdateFile(root, name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash, err := fileDigest(ctx, file)
	if err != nil || hash != pin.binary {
		return "", errUpdate
	}
	return filepath.Join(root.Name(), name), nil
}

func (d *releaseDelivery) privateVerifier(ctx context.Context, root *os.Root, showProgress bool) (string, error) {
	name, pin := privateVerifierAsset()
	return d.installPrivateVerifier(ctx, root, verifierPlatform(), name, pin, showProgress)
}

func (d *releaseDelivery) installPrivateVerifier(ctx context.Context, root *os.Root, platform, name string, pin verifierPin, showProgress bool) (string, error) {
	if pin.binary == "" {
		return "", errUpdate
	}
	// openCache serializes downloads and releases the kernel lock on process
	// exit. The permanent destination always wins if another process created it.
	cache, err := openCache(ctx, root.Name())
	if err != nil {
		return "", err
	}
	defer cache.close()
	directory, err := root.Stat(".")
	if err != nil || !os.SameFile(directory, cache.directoryIdentity) {
		return "", errCache
	}
	// A crash after linking the executable can leave both names. Unlink only
	// our reserved staging file under the lock, then validate the final name.
	if err := removeUpdateFile(cache, "gh.download"); err != nil {
		return "", err
	}
	if path, err := existingPrivateVerifier(ctx, root, name, pin); !errors.Is(err, os.ErrNotExist) {
		return path, err
	}
	base := "gh_" + verifierVersion + "_" + platform
	extension, entry := ".tar.gz", base+"/bin/gh"
	if strings.HasPrefix(platform, "windows_") {
		extension, entry = ".zip", "bin/gh.exe"
	}
	url := "https://github.com/cli/cli/releases/download/v" + verifierVersion + "/" + base + extension
	if showProgress {
		fmt.Fprintln(os.Stderr, "Downloading GitHub CLI for automatic update verification...")
	}
	res, err := d.request(ctx, url)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	// This bound applies only to the seven fixed verifier downloads.
	const maxVerifierArchive = 32 << 20
	data, err := io.ReadAll(io.LimitReader(res.Body, maxVerifierArchive+1))
	sum := sha256.Sum256(data)
	if err != nil || len(data) > maxVerifierArchive || hex.EncodeToString(sum[:]) != pin.archive {
		return "", errUpdate
	}

	tmp, err := root.OpenFile("gh.download", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return "", err
	}
	defer root.Remove("gh.download")
	defer tmp.Close()
	if err := extractArchiveBinary(ctx, bytes.NewReader(data), int64(len(data)), extension == ".zip", entry, tmp); err != nil {
		return "", err
	}
	hash, err := fileDigest(ctx, tmp)
	if err != nil || hash != pin.binary {
		return "", errUpdate
	}
	if err := tmp.Chmod(0700); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if !cache.validLock() {
		return "", errCache
	}
	// Link never overwrites an occupied or replaced destination.
	if err := root.Link("gh.download", name); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := root.Remove("gh.download"); err != nil {
		return "", err
	}
	return existingPrivateVerifier(ctx, root, name, pin)
}
