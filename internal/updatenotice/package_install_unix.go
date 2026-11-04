//go:build darwin || linux

package updatenotice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"
)

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
	cache, err := openCache(ctx, filepath.Join(bin.Name(), ".update"))
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
		previous, err = matchPackageBinary(ctx, current, state, source)
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
	case previous != source:
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

func matchPackageBinary(ctx context.Context, installation *updateInstallation, state updateState, source packageIdentity) (packageIdentity, error) {
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
	return packageIdentity{}, errCache
}

func packageInstalledVersion(ctx context.Context, bin *os.Root) (string, error) {
	current, err := openInstallation(filepath.Join(bin.Name(), "openai"))
	if err != nil {
		return "", err
	}
	defer current.close()
	cache, err := openCache(ctx, current.stateDirectory())
	if err != nil {
		return "", err
	}
	defer cache.close()
	state, err := loadUpdateState(cache, time.Now())
	if err != nil {
		return "", err
	}
	id, err := matchPackageBinary(ctx, current, state, packageIdentity{})
	if err != nil || !cache.validLock() || !cache.unchangedState() {
		return "", errCache
	}
	return id.Version, nil
}
