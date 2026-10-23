package updatenotice

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Only GoReleaser supplies these values. Ordinary builds and go install leave
// them empty, even when the application version is a stable release number.
var releaseVersion, releaseCommit string

var errPreparedUpdate = errors.New("prepared update is no longer usable")

const (
	automaticInterval = 4 * time.Hour
	workerTimeout     = 2 * time.Minute
	workerArgument    = "__openai_update_worker"
)

type updateState struct {
	Schema              int             `json:"schema"`
	Checked             int64           `json:"checked"`
	ActivationChecked   int64           `json:"activation_checked"`
	Starting            int64           `json:"starting"`
	Pending             *preparedUpdate `json:"pending,omitempty"`
	Applied             string          `json:"applied,omitempty"`
	Announced           bool            `json:"announced"`
	VerifierUnavailable bool            `json:"verifier_unavailable,omitempty"`
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
	}
	return state, nil
}

func due(now time.Time, checked int64) bool {
	return checked == 0 || now.Sub(time.Unix(checked, 0)) >= automaticInterval
}

// Automatic runs only after successful bare terminal help. It never performs
// network requests or waits for a worker. Unsupported installations keep notices.
func Automatic(ctx context.Context, current string) (string, bool) {
	installation, err := managedInstallation(current)
	if err != nil {
		return "", false
	}
	defer installation.close()
	if _, err := exec.LookPath("gh"); err != nil {
		return "", false
	}
	pin, enabled := updatePolicy(os.Getenv)
	if !enabled {
		return "", false
	}
	if ctx.Err() != nil {
		return "", true
	}
	cache, err := openCache(ctx, installation.stateDirectory())
	if err != nil {
		return "", true
	}
	now := time.Now()
	state, err := loadUpdateState(cache, now)
	if err != nil {
		cache.close()
		return "", true
	}
	message := ""
	if state.Applied == current && !state.Announced && (pin == "" || pin == current) && state.Pending != nil && state.Pending.Release.Commit == releaseCommit {
		digest, err := installation.digest(ctx)
		if err != nil || digest != state.Pending.BinaryHash {
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

// RunUpdateHelper handles the private, detached worker entrypoint. No update
// controls can turn an ordinary script invocation into this worker.
func RunUpdateHelper(argv []string) bool {
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
	installation, err := managedInstallation(argv[2])
	if err != nil {
		return true
	}
	defer installation.close()
	pin, enabled := updatePolicy(os.Getenv)
	if !enabled {
		return true
	}
	verifier, err := exec.LookPath("gh")
	if err != nil {
		return true
	}
	client := updateClient()
	defer client.CloseIdleConnections()
	worker := updateWorker{installation: installation, current: argv[2], pin: pin, now: time.Now(), delivery: &releaseDelivery{client: client, verifier: verifier}}
	_ = worker.run(ctx)
	return true
}

type updateDelivery interface {
	checkVerifier(context.Context, *noticeCache) error
	selectRelease(context.Context, string) (releaseSelection, error)
	prepare(context.Context, *noticeCache, releaseSelection) (string, error)
	stillPublished(context.Context, releaseSelection) error
}

type updateWorker struct {
	installation *updateInstallation
	current, pin string
	now          time.Time
	delivery     updateDelivery
}

func (worker updateWorker) run(ctx context.Context) error {
	cache, err := openCache(ctx, worker.installation.stateDirectory())
	if err != nil {
		return err
	}
	defer cache.close()
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
	for _, name := range []string{"archive", "bundle", "next", "next.tmp"} {
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
	binaryHash, err := worker.delivery.prepare(ctx, cache, selected)
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
	next, err := openUpdateFile(cache.root, "next")
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
	// Commit the receipt first. A crash before rename cannot announce success:
	// only an executing binary with the new release version consumes it.
	state.Applied, state.Announced = pending.Release.Version, false
	if err := cache.saveState(state); err != nil {
		return err
	}
	actual, err := cache.root.Lstat("next")
	if err != nil || !sameUpdateFile(info, actual) || !worker.policyUnchanged() || !worker.installation.unchanged() || !cache.validLock() || !cache.unchangedState() || ctx.Err() != nil {
		return errCache
	}
	// Both paths are beneath the same installation root. Rename is atomic on
	// supported systems. Existing processes continue using their original inode.
	return worker.installation.root.Rename(".update/next", "openai")
}

func discardPreparedUpdate(cache *noticeCache, state updateState) {
	state.Pending, state.Applied = nil, ""
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
