package updatenotice

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
)

// Older Mac installers did not write an enrollment receipt. Recognize their
// official signed executable without trusting a version string or running it.
func legacyPackageIdentity(ctx context.Context, cache *noticeCache, current *updateInstallation, hash string) (packageIdentity, error) {
	version, commit, ok := binaryReleaseStamp(current.file)
	stable, valid := stableVersion(version)
	if !ok || !valid || stable != version || !validCommit(commit) || !validDigest(hash) {
		return packageIdentity{}, errCache
	}
	// codesign requires a filesystem path. Give it a private snapshot of the
	// opened file. One reserved name bounds disk use after interrupted setup.
	const snapshot = "package-signature"
	if err := removeUpdateFile(cache, snapshot); err != nil {
		return packageIdentity{}, err
	}
	file, err := cache.root.OpenFile(snapshot, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return packageIdentity{}, err
	}
	defer file.Close()
	defer removeUpdateFile(cache, snapshot)
	if _, err := io.Copy(file, contextReader{ctx, io.NewSectionReader(current.file, 0, current.identity.Size())}); err != nil {
		return packageIdentity{}, err
	}
	// Require the OpenAI Developer ID Application signature and the CLI's
	// signing identifier. Ad hoc signatures cannot establish this identity.
	const requirement = `-R=anchor apple generic and identifier "openai" and certificate leaf[subject.OU] = "2DC432GLL2" and certificate leaf[field.1.2.840.113635.100.6.1.13] exists`
	name := filepath.Join(cache.directory, snapshot)
	cmd := exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", "--all-architectures", requirement, name)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LC_ALL=C"}
	if err := cmd.Run(); err != nil {
		return packageIdentity{}, errCache
	}
	digest, err := fileDigest(ctx, file)
	actual, statErr := file.Stat()
	named, nameErr := cache.root.Lstat(snapshot)
	if err != nil || digest != hash || statErr != nil || nameErr != nil || !sameUpdateFile(actual, named) ||
		!cache.validLock() || !current.unchanged() {
		return packageIdentity{}, errCache
	}
	return packageIdentity{Version: version, Commit: commit, Hash: hash}, nil
}
