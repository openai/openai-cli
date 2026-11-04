package updatenotice

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/openai/openai-cli/internal/darwinacl"
	"golang.org/x/sys/unix"
)

// Homebrew updates opt/gh to a new versioned keg. Persist only the selected
// prefix. Resolve and pin that keg again for each verifier session.
func runHomebrewVerifierHelper(argv []string) bool {
	if len(argv) != 3 || argv[1] != "__openai_homebrew_verifier" || os.Getenv("OPENAI_CLI_ARCHIVE_INSTALL") != "1" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), workerTimeout)
	defer cancel()
	status := "unsafe"
	prefix, err := filepath.EvalSymlinks(argv[2])
	if err == nil && automaticPlatform() && validCommit(releaseCommit) && validHomebrewPrefix(prefix) && filepath.IsAbs(argv[2]) {
		status = enrollHomebrewVerifier(ctx, prefix)
	}
	fmt.Println(status)
	return true
}

func validHomebrewPrefix(prefix string) bool {
	return filepath.IsAbs(prefix) && prefix != "/" && len(prefix) <= 1024 && filepath.Clean(prefix) == prefix && !strings.ContainsAny(prefix, "\x00\r\n")
}

func enrollHomebrewVerifier(ctx context.Context, prefix string) string {
	bin, err := openManagedBin()
	if err != nil {
		return "unsafe"
	}
	defer bin.Close()
	cache, err := openPackageCache(ctx, bin)
	if err != nil {
		return "unsafe"
	}
	defer cache.close()
	state, err := loadUpdateState(cache, time.Now())
	if err != nil || !protectedHomebrewState(cache) {
		return "unsafe"
	}
	delivery := &releaseDelivery{}
	defer delivery.closeVerifier()
	err = delivery.openHomebrewVerifier(ctx, prefix, cache.directory)
	if errors.Is(err, os.ErrNotExist) {
		return "missing"
	}
	if err != nil {
		return "unsafe"
	}
	if err := delivery.checkVerifierHelp(ctx, cache.directory); err != nil {
		return "unavailable"
	}
	state.HomebrewPrefix = prefix
	if err := cache.saveState(state); err != nil {
		return "unsafe"
	}
	return "ready"
}

func mayHaveHomebrewVerifier(directory string) bool {
	info, err := os.Lstat(filepath.Join(directory, stateName))
	return err == nil && privateFile(info)
}

func selectEnrolledHomebrewVerifier(ctx context.Context, d *releaseDelivery, cache *noticeCache) bool {
	var receipt struct {
		Prefix string `json:"homebrew_prefix"`
	}
	if json.Unmarshal(cache.stateData, &receipt) != nil || !validHomebrewPrefix(receipt.Prefix) || !protectedHomebrewState(cache) {
		return false
	}
	if useHomebrewVerifier(ctx, d, receipt.Prefix, cache.directory) {
		return true
	}
	d.selectHomebrewFallback(ctx, cache.directory)
	return false
}

func useHomebrewVerifier(ctx context.Context, d *releaseDelivery, prefix, work string) bool {
	previous := d.verifier
	if d.openHomebrewVerifier(ctx, prefix, work) == nil {
		if d.checkVerifierHelp(ctx, work) == nil {
			return true
		}
		d.closeVerifier()
	}
	d.verifier = previous
	return false
}

// The generic installer probe never creates a cache, enrolls a provider, or
// writes managed state. Its temporary verifier exists only during this probe.
func probeHomebrewVerifier(ctx context.Context, d *releaseDelivery, work string) bool {
	prefix := readHomebrewPrefix(ctx)
	if prefix != "" && useHomebrewVerifier(ctx, d, prefix, work) {
		return true
	}
	if prefix != "" || os.Getenv("OPENAI_CLI_HOMEBREW_SETUP") == "1" {
		d.selectHomebrewFallback(ctx, work)
	}
	return false
}

func readHomebrewPrefix(ctx context.Context) string {
	bin, err := openManagedBin()
	if err != nil {
		return ""
	}
	defer bin.Close()
	info, err := bin.Lstat(".update")
	if err != nil || !info.IsDir() || !ownedUpdatePath(info) || !privateMode(info) {
		return ""
	}
	root, err := bin.OpenRoot(".update")
	if err != nil {
		return ""
	}
	defer root.Close()
	cache := &noticeCache{ctx: ctx, root: root, directory: filepath.Join(bin.Name(), ".update"), directoryIdentity: info}
	state, err := loadUpdateState(cache, time.Now())
	if err != nil {
		return ""
	}
	return state.HomebrewPrefix
}

// A selected Homebrew installation without a suitable formula can reuse a
// protected PATH program. Pin the exact descriptor we checked for this run.
func (d *releaseDelivery) selectHomebrewFallback(ctx context.Context, work string) {
	original := d.verifier
	d.verifier = ""
	if ctx.Err() != nil {
		return
	}
	path := original
	for links := 0; links <= 40; links++ {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return
		}
		parent, err := openHomebrewPath(filepath.Dir(path))
		if err != nil {
			return
		}
		base := filepath.Base(path)
		entry, err := parent.Lstat(base)
		if err != nil || !trustedHomebrewOwner(entry) {
			parent.Close()
			return
		}
		if entry.Mode()&os.ModeSymlink != 0 {
			target, err := parent.Readlink(base)
			parent.Close()
			if err != nil {
				return
			}
			if filepath.IsAbs(target) {
				path = target
			} else {
				path = filepath.Join(filepath.Dir(path), target)
			}
			continue
		}
		file, err := parent.OpenFile(base, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		parent.Close()
		if err != nil {
			return
		}
		defer file.Close()
		actual, err := file.Stat()
		selected, lookupErr := os.Stat(original)
		if err != nil || lookupErr != nil || !os.SameFile(actual, entry) || !os.SameFile(actual, selected) || !safeHomebrewExecutable(file, actual) {
			return
		}
		_ = d.snapshotHomebrewVerifier(ctx, file, work)
		return
	}
}

func safeHomebrewExecutable(file *os.File, info os.FileInfo) bool {
	return info.Mode().IsRegular() && trustedHomebrewOwner(info) && info.Mode().Perm()&0022 == 0 && info.Mode().Perm()&0111 != 0 && info.Sys().(*syscall.Stat_t).Nlink == 1 && darwinacl.Check(file, darwinacl.ReadOnly) == nil
}

func validHomebrewReceipt(cache *noticeCache, prefix string) bool {
	return validHomebrewPrefix(prefix) && protectedHomebrewState(cache)
}

func protectedHomebrewState(cache *noticeCache) bool {
	if (cache.lock != nil && !cache.validLock()) || !cache.unchangedState() {
		return false
	}
	trusted, err := openHomebrewPath(cache.directory)
	if err != nil {
		return false
	}
	named, err := trusted.Stat(".")
	trusted.Close()
	if err != nil || !os.SameFile(cache.directoryIdentity, named) {
		return false
	}
	files := map[string]os.FileInfo{".": cache.directoryIdentity, stateName: cache.stateIdentity}
	if cache.lock != nil {
		files[".lock"] = cache.identity
	}
	for name, want := range files {
		f, err := cache.root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if name == stateName && os.IsNotExist(err) && cache.stateIdentity == nil {
			continue
		}
		if err != nil {
			return false
		}
		info, err := f.Stat()
		valid := err == nil && want != nil && os.SameFile(info, want) && ownedUpdatePath(info) && darwinacl.Check(f, darwinacl.ReadOnly) == nil
		f.Close()
		if !valid {
			return false
		}
	}
	return true
}

var homebrewKegVersion = regexp.MustCompile(`^[0-9][0-9A-Za-z._-]*$`)

func (d *releaseDelivery) openHomebrewVerifier(ctx context.Context, prefix, work string) error {
	bin, err := openHomebrewFormula(prefix)
	if err != nil {
		return err
	}
	defer bin.Close()
	info, err := bin.Lstat("gh")
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errCache
	}
	file, err := bin.OpenFile("gh", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(actual, info) || !safeHomebrewExecutable(file, actual) {
		return errCache
	}
	build, err := buildinfo.Read(file)
	if err != nil || build.Path != "github.com/cli/cli/v2/cmd/gh" || build.Main.Path != "github.com/cli/cli/v2" {
		return errCache
	}
	return d.snapshotHomebrewVerifier(ctx, file, work)
}

func (d *releaseDelivery) snapshotHomebrewVerifier(ctx context.Context, file *os.File, work string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// Keep one crash-recoverable snapshot under the updater lock. Generic
	// capability probes receive their own temporary directory.
	dir, err := prepareHomebrewScratch(work)
	if err != nil {
		return err
	}
	defer dir.Close()
	target := filepath.Join(dir.Name(), "gh")
	cleanup := func() {
		_ = os.Remove(target)
		_ = os.Remove(filepath.Join(dir.Name(), homebrewScratchMarker))
		_ = os.Remove(dir.Name())
	}
	// The APFS clone has its own inode and shares data blocks with Homebrew.
	// BSD cp preserves quarantine on filesystems without cloning.
	err = unix.Fclonefileat(int(file.Fd()), int(dir.Fd()), "gh", unix.CLONE_NOOWNERCOPY)
	if err != nil {
		if !errors.Is(err, syscall.ENOSYS) && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.EXDEV) && !errors.Is(err, syscall.ENOTSUP) && !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
			cleanup()
			return err
		}
		err = copyHomebrewVerifier(ctx, file, target)
	}
	if err != nil {
		cleanup()
		return err
	}
	d.verifier, d.verifierCleanup = target, cleanup
	return nil
}

const homebrewScratchMarker = ".openai-verifier-session-v1"

// The snapshot is disposable. Never follow or remove unrelated state after
// a killed installer. One empty marker identifies our private scratch area.
func prepareHomebrewScratch(work string) (*os.File, error) {
	physical, err := filepath.EvalSymlinks(work)
	if err != nil {
		return nil, err
	}
	parent, err := openHomebrewPath(physical)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	work = physical
	path := filepath.Join(work, ".homebrew-verifier")
	if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	dir, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			dir.Close()
		}
	}()
	info, err := dir.Stat()
	if err != nil || !ownedUpdatePath(info) || !privateMode(info) || darwinacl.Check(dir, darwinacl.DenyOnly) != nil {
		return nil, errCache
	}
	names, err := dir.Readdirnames(3)
	if err != nil && err != io.EOF {
		return nil, err
	}
	if len(names) == 0 {
		f, err := os.OpenFile(filepath.Join(path, homebrewScratchMarker), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
	} else {
		m, err := os.Lstat(filepath.Join(path, homebrewScratchMarker))
		if err != nil || !m.Mode().IsRegular() || !ownedUpdatePath(m) || !privateMode(m) || m.Size() != 0 {
			return nil, errCache
		}
		for _, name := range names {
			if name == homebrewScratchMarker {
				continue
			}
			if name != "gh" {
				return nil, errCache
			}
			old, err := os.Lstat(filepath.Join(path, name))
			if err != nil || !old.Mode().IsRegular() || !ownedUpdatePath(old) {
				return nil, errCache
			}
		}
		if err := os.Remove(filepath.Join(path, "gh")); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	keep = true
	return dir, nil
}

func copyHomebrewVerifier(ctx context.Context, file *os.File, target string) error {
	command := exec.CommandContext(ctx, "/bin/cp", "/dev/fd/3", target)
	command.ExtraFiles = []*os.File{file}
	command.Env = []string{"PATH=/usr/bin:/bin"}
	if err := command.Run(); err != nil {
		return err
	}
	copy, err := os.Open(target)
	if err != nil {
		return err
	}
	defer copy.Close()
	sourceHash, err := fileDigest(ctx, file)
	if err != nil {
		return err
	}
	copyHash, err := fileDigest(ctx, copy)
	if err != nil || copyHash != sourceHash || !preservedQuarantine(file, copy) || darwinacl.Check(copy, darwinacl.ReadOnly) != nil {
		return errCache
	}
	return copy.Chmod(0700)
}

// A filesystem can accept the bytes and still discard quarantine.
func preservedQuarantine(source, target *os.File) bool {
	const name = "com.apple.quarantine"
	size, err := unix.Fgetxattr(int(source.Fd()), name, nil)
	if errors.Is(err, unix.ENOATTR) || errors.Is(err, unix.ENOTSUP) {
		return true
	}
	if err != nil || size > 64<<10 {
		return false
	}
	a, b := make([]byte, size), make([]byte, size)
	n, err := unix.Fgetxattr(int(source.Fd()), name, a)
	if err != nil || n != size {
		return false
	}
	n, err = unix.Fgetxattr(int(target.Fd()), name, b)
	return err == nil && n == size && bytes.Equal(a, b)
}

func trustedHomebrewOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && (stat.Uid == 0 || stat.Uid == uint32(os.Geteuid()))
}

// Walk regular directories through opened roots. Homebrew commonly grants the
// admin group write access to opt and Cellar. Do not trust names across those
// directories after opening their descendants.
func openHomebrewFormula(prefix string) (*os.Root, error) {
	if !validHomebrewPrefix(prefix) {
		return nil, errCache
	}
	root, err := openHomebrewPath(prefix)
	if err != nil {
		return nil, err
	}
	defer func() { root.Close() }()
	opt, err := homebrewDirectory(root, "opt", true)
	if err != nil {
		return nil, err
	}
	defer opt.Close()
	link, err := opt.Lstat("gh")
	if err != nil {
		return nil, err
	}
	if link.Mode()&os.ModeSymlink == 0 || !trustedHomebrewOwner(link) {
		return nil, errCache
	}
	value, err := opt.Readlink("gh")
	if err != nil {
		return nil, err
	}
	version := filepath.Base(value)
	if !homebrewKegVersion.MatchString(version) || (value != "../Cellar/gh/"+version && value != filepath.Join(prefix, "Cellar", "gh", version)) {
		return nil, errCache
	}
	for _, part := range []string{"Cellar", "gh", version, "bin"} {
		next, err := homebrewDirectory(root, part, part == "Cellar")
		if err != nil {
			return nil, err
		}
		root.Close()
		root = next
	}
	return root.OpenRoot(".")
}

func openHomebrewPath(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errCache
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return nil, err
	}
	if path == "/" {
		return root, nil
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := homebrewDirectory(root, part, false)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}

func homebrewDirectory(parent *os.Root, name string, allowAdmin bool) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !trustedHomebrewOwner(info) {
		return nil, errCache
	}
	stat := info.Sys().(*syscall.Stat_t)
	shared := info.Mode().Perm() & 0022
	if shared != 0 && !(stat.Uid == 0 && info.Mode()&os.ModeSticky != 0) && !(allowAdmin && shared == 0020 && stat.Gid == 80) {
		return nil, errCache
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	f, err := root.Open(".")
	if err != nil {
		root.Close()
		return nil, err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) || darwinacl.Check(f, darwinacl.ReadOnly) != nil {
		root.Close()
		return nil, errCache
	}
	return root, nil
}
