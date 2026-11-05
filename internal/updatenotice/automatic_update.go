package updatenotice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Only GoReleaser supplies these values. Ordinary builds and go install leave
// them empty, even when the application version is a stable release number.
var releaseVersion, releaseCommit string

var errBusyUpdateImage = errors.New("update executable is busy")
var errPreparedUpdate = errors.New("prepared update is no longer usable")

const (
	automaticInterval = 4 * time.Hour
	workerTimeout     = 2 * time.Minute
	workerArgument    = "__openai_update_worker"
)

type packageIdentity struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Hash    string `json:"sha256"`
}

type updateState struct {
	HomebrewPrefix      string            `json:"homebrew_prefix,omitempty"`
	PackageSources      []packageIdentity `json:"package_sources,omitempty"`
	Schema              int               `json:"schema"`
	Checked             int64             `json:"checked"`
	ActivationChecked   int64             `json:"activation_checked"`
	Starting            int64             `json:"starting"`
	Pending             *preparedUpdate   `json:"pending,omitempty"`
	Applied             string            `json:"applied,omitempty"`
	AppliedCommit       string            `json:"applied_commit,omitempty"`
	AppliedHash         string            `json:"applied_hash,omitempty"`
	Announced           bool              `json:"announced"`
	VerifierUnavailable bool              `json:"verifier_unavailable,omitempty"`
}

type preparedUpdate struct {
	Release      releaseSelection `json:"release"`
	BinaryHash   string           `json:"binary_hash"`
	PreviousHash string           `json:"previous_hash"`
}

func updatePolicy(getenv func(string) string) (pin string, enabled bool) {
	for _, key := range []string{"OPENAI_CLI_NO_UPDATE_CHECK", "OPENAI_CLI_NO_AUTO_UPDATE"} {
		value := strings.ToLower(getenv(key))
		if value != "" && value != "0" && value != "false" {
			return "", false
		}
	}
	if value := getenv("OPENAI_CLI_VERSION_PIN"); value != "" {
		pin, enabled = stableVersion(value)
		return pin, enabled
	}
	return "", true
}

func allowedVersion(version, current, pin string) bool {
	if pin != "" {
		return version == pin && version != current
	}
	return newerVersion(version, current)
}

func loadUpdateState(cache *noticeCache, now time.Time) (updateState, error) {
	state := updateState{Schema: 1}
	data, err := cache.readState()
	if err != nil || data == nil {
		return state, err
	}
	if json.Unmarshal(data, &state) != nil || state.Schema != 1 || state.Checked < 0 || state.Checked > now.Unix() || state.ActivationChecked < 0 || state.ActivationChecked > now.Unix() || state.Starting < 0 || state.Starting > now.Unix() {
		return state, errCache
	}
	if state.HomebrewPrefix != "" && !validHomebrewReceipt(cache, state.HomebrewPrefix) {
		return state, errCache
	}
	if state.Applied != "" {
		v, ok := stableVersion(state.Applied)
		if !ok || v != state.Applied {
			return state, errCache
		}
	}
	if p := state.Pending; p != nil {
		if !p.Release.valid() || !validDigest(p.BinaryHash) || !validDigest(p.PreviousHash) {
			return state, errCache
		}
		// Older receipts kept the installed artifact's identity in Pending.
		if state.Applied == p.Release.Version && state.AppliedCommit == "" && state.AppliedHash == "" {
			state.AppliedCommit, state.AppliedHash = p.Release.Commit, p.BinaryHash
		}
	}
	if (state.AppliedHash != "" || state.AppliedCommit != "") && (!validDigest(state.AppliedHash) || !validCommit(state.AppliedCommit)) {
		return state, errCache
	}
	return state, nil
}

func due(now time.Time, checked int64) bool {
	return checked == 0 || now.Sub(time.Unix(checked, 0)) >= automaticInterval
}

// UpdateSilently schedules only managed updates. Ordinary API commands never
// fall back to release notices or consume the next bare-help confirmation.
func UpdateSilently(ctx context.Context, current string) {
	automatic(ctx, current, false)
}

// automatic starts detached work without waiting for network requests.
func automatic(ctx context.Context, current string, announce bool) (string, bool) {
	pin, enabled := updatePolicy(os.Getenv)
	if !enabled || ctx.Err() != nil {
		return "", false
	}
	installation, err := managedInstallation(current, false)
	if err != nil {
		return "", false
	}
	defer installation.close()
	haveVerifier := systemUpdateVerifier() != "" || verifierEnrolled(installation.root.Name())
	if !haveVerifier && !mayHaveHomebrewVerifier(installation.stateDirectory()) {
		return "", false
	}
	cache, err := openManagedUpdateCache(ctx, installation)
	if err != nil {
		return "", true
	}
	now := time.Now()
	state, err := loadUpdateState(cache, now)
	if err != nil {
		cache.close()
		return "", true
	}
	if !haveVerifier && state.HomebrewPrefix == "" {
		cache.close()
		return "", false
	}
	message := ""
	if announce && state.Applied == current && !state.Announced && (pin == "" || pin == current) && state.AppliedCommit == releaseCommit {
		digest, err := installation.digest(ctx)
		if err != nil || digest != state.AppliedHash {
			cache.close()
			return "", true
		}
		state.Announced = true
		if cache.saveState(state) == nil {
			message = "Updated to v" + current + "."
		}
	}
	start := due(now, state.Checked)
	if state.Pending != nil && allowedVersion(state.Pending.Release.Version, current, pin) {
		start = due(now, state.ActivationChecked)
	}
	if state.Starting != 0 && now.Sub(time.Unix(state.Starting, 0)) < workerTimeout+3*time.Second {
		start = false
	}
	if start {
		// Reserve process startup while holding the lock. Simultaneous launches
		// cannot accumulate helpers before the first child acquires its lock.
		state.Starting = now.Unix()
		start = cache.saveState(state) == nil
	}
	cache.close()
	if start && installation.unchanged() && ctx.Err() == nil {
		startUpdateWorker(installation.path, current)
	}
	return message, message != "" || !state.VerifierUnavailable
}

// RunUpdateHelper handles the private installer probe and detached update worker.
func RunUpdateHelper(argv []string) bool {
	if runPackageInstallHelper(argv) || runLinuxPackageHelper(argv) || runHomebrewVerifierHelper(argv) {
		return true
	}
	if len(argv) == 4 && (argv[1] == "__openai_archive_zsh_profile" || argv[1] == "__openai_zsh_login_profile") && os.Getenv("OPENAI_CLI_ARCHIVE_INSTALL") == "1" {
		if enableZshPath(argv[2], argv[3], argv[1] == "__openai_zsh_login_profile") {
			fmt.Fprintln(os.Stdout, "ready")
		}
		return true
	}
	if len(argv) == 2 && argv[1] == "__openai_setup_update_verifier" && os.Getenv("OPENAI_CLI_ARCHIVE_INSTALL") == "1" {
		ctx, cancel := context.WithTimeout(context.Background(), workerTimeout)
		defer cancel()
		if err := setupUpdateVerifier(ctx); err == nil {
			fmt.Fprintln(os.Stdout, "ready")
		} else {
			fmt.Fprintln(os.Stderr, verifierSetupAdvice(err))
		}
		return true
	}
	if len(argv) == 2 && argv[1] == "__openai_archive_verifier" && os.Getenv("OPENAI_CLI_ARCHIVE_INSTALL") == "1" {
		fmt.Fprintln(os.Stdout, archiveVerifierStatus())
		return true
	}
	if len(argv) < 2 || argv[1] != workerArgument {
		return false
	}
	if len(argv) != 3 || os.Getenv("OPENAI_CLI_UPDATE_WORKER") != "1" || argv[2] != releaseVersion {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), workerTimeout)
	defer cancel()
	watchdog := time.AfterFunc(workerTimeout+3*time.Second, func() { os.Exit(0) })
	defer watchdog.Stop()
	if relayUpdateWorker(ctx, argv[2]) {
		return true
	}
	installation, err := managedInstallation(argv[2], true)
	if err != nil {
		return true
	}
	defer installation.close()
	pin, enabled := updatePolicy(os.Getenv)
	if !enabled {
		return true
	}
	verifier := systemUpdateVerifier()
	privateVerifier := verifierEnrolled(installation.root.Name())
	if verifier == "" && !privateVerifier && !mayHaveHomebrewVerifier(installation.stateDirectory()) {
		return true
	}
	client := updateClient()
	defer client.CloseIdleConnections()
	delivery := &releaseDelivery{client: client, verifier: verifier, privateVerifierEnabled: privateVerifier}
	worker := updateWorker{installation: installation, current: argv[2], pin: pin, now: time.Now(), delivery: delivery}
	_ = worker.run(ctx)
	return true
}

type updateDelivery interface {
	closeVerifier()
	checkVerifier(context.Context, *noticeCache) error
	selectRelease(context.Context, string) (releaseSelection, error)
	prepare(context.Context, *noticeCache, releaseSelection, *os.File) (string, error)
	stillPublished(context.Context, releaseSelection) error
}

type updateWorker struct {
	installation *updateInstallation
	current, pin string
	now          time.Time
	delivery     updateDelivery
}

func (worker updateWorker) run(ctx context.Context) error {
	cache, err := openManagedUpdateCache(ctx, worker.installation)
	if err != nil {
		return err
	}
	defer cache.close()
	defer worker.delivery.closeVerifier()
	state, err := loadUpdateState(cache, worker.now)
	if err != nil || !worker.installation.unchanged() {
		return errCache
	}
	state.Starting = 0
	if pending := state.Pending; pending != nil && allowedVersion(pending.Release.Version, worker.current, worker.pin) {
		if !due(worker.now, state.ActivationChecked) {
			return nil
		}
		state.ActivationChecked = worker.now.Unix()
		if err := cache.saveState(state); err != nil {
			return err
		}
		if err := worker.delivery.stillPublished(ctx, pending.Release); err != nil {
			if errors.Is(err, errWithdrawn) {
				discardPreparedUpdate(cache, state)
			}
			return err
		}
		err := worker.activate(ctx, cache, state)
		if errors.Is(err, errBusyUpdateImage) && ctx.Err() == nil {
			// An existing CLI or antivirus scanner still has an open executable.
			// Keep the prepared file and retry on later use without another download.
			state.ActivationChecked = worker.now.Add(time.Minute - automaticInterval).Unix()
			return cache.saveState(state)
		}
		if errors.Is(err, errPreparedUpdate) {
			discardPreparedUpdate(cache, state)
		}
		return err
	}
	if !due(worker.now, state.Checked) {
		return nil
	}
	state.Checked, state.ActivationChecked = worker.now.Unix(), 0
	state.Pending = nil
	// Check capabilities in the detached worker, before downloading anything.
	// Persist fallback first so failed or interrupted probes retain notices.
	state.VerifierUnavailable = true
	if err := cache.saveState(state); err != nil {
		return err
	}
	if err := worker.delivery.checkVerifier(ctx, cache); err != nil {
		return err
	}
	state.VerifierUnavailable = false
	if err := cache.saveState(state); err != nil {
		return err
	}
	// Fixed owned temporary names keep interrupted work bounded. Remove only
	// regular files inside the locked private directory, never arbitrary trees.
	for _, name := range []string{"archive", "bundle", updateBundleName, "next", "next.tmp"} {
		if err := removeUpdateFile(cache, name); err != nil {
			return err
		}
	}
	selected, err := worker.delivery.selectRelease(ctx, worker.pin)
	if err != nil {
		return err
	}
	if !selected.valid() {
		return errUpdate
	}
	if !allowedVersion(selected.Version, worker.current, worker.pin) {
		return nil
	}
	previousHash, err := worker.installation.digest(ctx)
	if err != nil {
		return err
	}
	binaryHash, err := worker.delivery.prepare(ctx, cache, selected, worker.installation.file)
	if err != nil {
		return err
	}
	if !worker.installation.unchanged() || !worker.policyUnchanged() {
		return errCache
	}
	state.Pending = &preparedUpdate{Release: selected, BinaryHash: binaryHash, PreviousHash: previousHash}
	return cache.saveState(state)
}

func (worker updateWorker) activate(ctx context.Context, cache *noticeCache, state updateState) error {
	if !worker.policyUnchanged() {
		return errUpdate
	}
	pending := state.Pending
	previous, err := worker.installation.digest(ctx)
	if err != nil {
		return err
	}
	if previous != pending.PreviousHash {
		return errPreparedUpdate
	}
	if _, err := cache.root.Lstat("next"); os.IsNotExist(err) {
		return errPreparedUpdate
	}
	next, err := openActivationImage(cache.root)
	if err != nil {
		return err
	}
	defer next.Close()
	info, err := next.Stat()
	if err != nil {
		return err
	}
	digest, err := fileDigest(ctx, next)
	if err != nil {
		return err
	}
	if digest != pending.BinaryHash || !binaryIdentity(next, pending.Release.Version, pending.Release.Commit) {
		return errPreparedUpdate
	}
	// Older staged updates may lack trust metadata. Check the installed
	// executable again before activating the exact verified replacement.
	if err := preservePreparedImageTrust(worker.installation.file, next); err != nil {
		return err
	}
	active, info, err := pinActivationImage(ctx, cache.root, next, info, pending.BinaryHash)
	if err != nil {
		return err
	}
	if active != next {
		defer active.Close()
	}
	// Commit the receipt first. A crash before rename cannot announce success:
	// only an executing binary with the new release version consumes it.
	state.Applied, state.Announced = pending.Release.Version, false
	state.AppliedCommit, state.AppliedHash = pending.Release.Commit, pending.BinaryHash
	if err := cache.saveState(state); err != nil {
		return err
	}
	replace := func() error {
		actual, err := cache.root.Lstat("next")
		if err != nil || !sameUpdateFile(info, actual) || !safeOpenedUpdateFile(active) || !worker.policyUnchanged() || !worker.installation.unchanged() || !cache.validLock() || !cache.unchangedState() || ctx.Err() != nil {
			return errCache
		}
		return renameActivationImage(worker.installation.root, active, filepath.Base(worker.installation.path))
	}
	return replaceUpdateImage(ctx, replace)
}

func discardPreparedUpdate(cache *noticeCache, state updateState) {
	if state.Pending.Release.Version == state.Applied {
		// A failed rename may have written a receipt for this staged release.
		state.Applied, state.AppliedHash, state.AppliedCommit = "", "", ""
	}
	state.Pending = nil
	if cache.saveState(state) == nil {
		_ = removeUpdateFile(cache, "next")
	}
}

func (worker updateWorker) policyUnchanged() bool {
	pin, enabled := updatePolicy(os.Getenv)
	return enabled && pin == worker.pin
}

func removeUpdateFile(cache *noticeCache, name string) error {
	info, err := cache.root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !privateFile(info) || !cache.validLock() {
		return errCache
	}
	return cache.root.Remove(name)
}

func (installation *updateInstallation) stateDirectory() string {
	return filepath.Join(filepath.Dir(installation.path), ".update")
}
