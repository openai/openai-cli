//go:build darwin || linux

package updatenotice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The installer runs the transaction from a private copy of the new release.
func runPackageInstallHelper(argv []string) bool {
	if len(argv) != 2 || os.Getenv("OPENAI_CLI_ARCHIVE_INSTALL") != "1" ||
		(argv[1] != "__openai_package_current" && argv[1] != "__openai_package_install") {
		return false
	}
	if !automaticPlatform() {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), workerTimeout+10*time.Second)
	defer cancel()
	bin, err := openManagedBin()
	if err != nil {
		return true
	}
	defer bin.Close()
	var active string
	if argv[1] == "__openai_package_current" {
		active, err = packageInstalledVersion(ctx, bin)
	} else {
		path, pathErr := os.Executable()
		if pathErr != nil {
			return true
		}
		staged, openErr := openInstallation(path)
		if openErr != nil {
			return true
		}
		defer staged.close()
		active, err = installPackageRelease(ctx, bin, staged, releaseVersion, releaseCommit)
	}
	if err == nil && active != "" {
		fmt.Println(active)
	}
	return true
}

// installPackageRelease serializes explicit package installs with automatic
// activation. The package manager retains its own executable.
func installPackageRelease(ctx context.Context, bin *os.Root, staged *updateInstallation, version, commit string) (string, error) {
	relative, err := filepath.Rel(bin.Name(), staged.path)
	dir, file, ok := strings.Cut(relative, "/")
	v, stable := stableVersion(version)
	if err != nil || !ok || file != "openai" || !strings.HasPrefix(dir, ".openai-install.") ||
		!stable || v != version || !validCommit(commit) || !binaryIdentity(staged.file, version, commit) {
		return "", errCache
	}
	digest, err := staged.digest(ctx)
	if err != nil {
		return "", err
	}
	source := packageIdentity{version, commit, digest}
	cache, err := openPackageCache(ctx, bin)
	if err != nil {
		return "", err
	}
	defer cache.close()
	state, err := loadUpdateState(cache, time.Now())
	if err != nil {
		return "", err
	}
	var current *updateInstallation
	var previous packageIdentity
	if _, err := bin.Lstat("openai"); !os.IsNotExist(err) {
		current, err = openInstallation(filepath.Join(bin.Name(), "openai"))
		if err != nil {
			return "", err
		}
		defer current.close()
		// Identical bytes can enroll an existing official archive installation.
		previous, err = matchPackageBinary(ctx, cache, current, state, source)
		if err != nil {
			return "", err
		}
		if previous != source && !newerVersion(source.Version, previous.Version) {
			return previous.Version, nil
		}
	}

	// Write both possible identities before renaming. An interrupted explicit
	// upgrade can resume with either the original or the replacement on disk.
	state.PackageSources = []packageIdentity{source}
	if previous != (packageIdentity{}) && previous != source {
		state.PackageSources = append(state.PackageSources, previous)
	}
	if previous != source {
		// An explicit installation must never announce an automatic update.
		state.Pending, state.Applied, state.AppliedCommit, state.AppliedHash = nil, "", "", ""
		state.Announced = false
	}
	if err := cache.saveState(state); err != nil {
		return "", err
	}
	namedSource, err := bin.Lstat(relative)
	if err != nil || !sameUpdateFile(staged.identity, namedSource) || !staged.unchanged() ||
		!cache.validLock() || !cache.unchangedState() || ctx.Err() != nil {
		return "", errCache
	}
	switch {
	case current == nil:
		// Preserve an occupied name. The installer's marker can recover a
		// kill between linking and unlinking the same temporary inode.
		if err := bin.Link(relative, "openai"); err != nil {
			return "", err
		}
		if err := bin.Remove(relative); err != nil {
			return "", err
		}
	case !current.unchanged():
		return "", errCache
	case previous == source:
		// The same verified package can restore its original marking to a
		// managed copy that an earlier updater installed without it.
		if err := preservePreparedImageTrust(staged.file, current.file); err != nil {
			return "", err
		}
	default:
		if err := preservePreparedImageTrust(current.file, staged.file); err != nil {
			return "", err
		}
		namedCurrent, err := bin.Lstat("openai")
		if err != nil || !sameUpdateFile(current.identity, namedCurrent) {
			return "", errCache
		}
		if err := bin.Rename(relative, "openai"); err != nil {
			return "", err
		}
	}
	return version, nil
}

func matchPackageBinary(ctx context.Context, cache *noticeCache, installation *updateInstallation, state updateState, source packageIdentity) (packageIdentity, error) {
	hash, err := installation.digest(ctx)
	if err != nil {
		return packageIdentity{}, err
	}
	if len(state.PackageSources) > 2 {
		return packageIdentity{}, errCache
	}
	candidates := append([]packageIdentity{source, {state.Applied, state.AppliedCommit, state.AppliedHash}}, state.PackageSources...)
	for _, id := range candidates {
		v, ok := stableVersion(id.Version)
		if ok && v == id.Version && validCommit(id.Commit) && id.Hash == hash && binaryIdentity(installation.file, v, id.Commit) && installation.unchanged() {
			return id, nil
		}
	}
	return legacyPackageIdentity(ctx, cache, installation, hash)
}

func packageInstalledVersion(ctx context.Context, bin *os.Root) (string, error) {
	cache, err := openPackageCache(ctx, bin)
	if err != nil {
		return "", err
	}
	defer cache.close()
	current, err := openInstallation(filepath.Join(bin.Name(), "openai"))
	if err != nil {
		return "", err
	}
	defer current.close()
	state, err := loadUpdateState(cache, time.Now())
	if err != nil {
		return "", err
	}
	id, err := matchPackageBinary(ctx, cache, current, state, packageIdentity{})
	if err != nil || !cache.validLock() || !cache.unchangedState() {
		return "", errCache
	}
	return id.Version, nil
}

// An explicit installation can wait for a running update. Interactive API
// commands still use the nonblocking cache lock.
func openPackageCache(ctx context.Context, bin *os.Root) (*noticeCache, error) {
	dir := filepath.Join(bin.Name(), ".update")
	cache, err := openCache(ctx, dir)
	if !errors.Is(err, errCacheBusy) {
		return cache, err
	}
	fmt.Fprintln(os.Stderr, "Waiting for the current OpenAI CLI update to finish...")
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			cache, err := openCache(ctx, dir)
			if !errors.Is(err, errCacheBusy) {
				return cache, err
			}
		}
	}
}
