//go:build darwin || linux

package updatenotice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const syntheticCommit = "1111111111111111111111111111111111111111"

// Keep build inputs stable while tests vary update policy and verifier credentials.
var updateFixtureEnvironment = os.Environ()

var updateFixtureBuilds = struct {
	sync.Mutex
	data map[updateFixtureBuildKey][]byte
}{data: make(map[updateFixtureBuildKey][]byte)}

type updateFixtureBuildKey struct {
	toolchain, source, flags, environment string
}

func syntheticSelection() releaseSelection {
	return releaseSelection{Version: "1.0.1", Commit: syntheticCommit, ReleaseID: 1, AssetID: 2, Digest: strings.Repeat("a", 64), Size: 10, Updated: "2026-10-22T00:00:00Z"}
}

func buildUpdateFixture(t *testing.T, directory, version string) []byte {
	t.Helper()
	const program = "package main\nfunc main() {}\n"
	source := filepath.Join(directory, "main.go")
	if err := os.WriteFile(source, []byte(program), 0600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(directory, "fixture-"+version)
	flags := []string{"build", "-ldflags", "-X " + stampPrefix + "releaseVersion=" + version + " -X " + stampPrefix + "releaseCommit=" + syntheticCommit}
	command := exec.CommandContext(t.Context(), "go", append(flags, "-o", destination, source)...)
	command.Env = updateFixtureEnvironment
	key := updateFixtureBuildKey{command.Path, program, strings.Join(flags, "\x00"), strings.Join(command.Env, "\x00")}
	updateFixtureBuilds.Lock()
	defer updateFixtureBuilds.Unlock()
	if data, exists := updateFixtureBuilds.data[key]; exists {
		if err := os.WriteFile(destination, data, 0700); err != nil {
			t.Fatal(err)
		}
		return bytes.Clone(data)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v: %s", err, output)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	updateFixtureBuilds.data[key] = data
	return bytes.Clone(data)
}

func TestUpdateFixtureBuildIdentityAndIsolation(t *testing.T) {
	first := buildUpdateFixture(t, t.TempDir(), "1.0.1")
	for _, version := range []string{"1.0.1", "9.9.9"} {
		directory := t.TempDir()
		data := buildUpdateFixture(t, directory, version)
		if bytes.Equal(first, data) != (version == "1.0.1") {
			t.Fatalf("fixture bytes did not preserve version %s", version)
		}
		file, err := os.Open(filepath.Join(directory, "fixture-"+version))
		if err != nil {
			t.Fatal(err)
		}
		matches := binaryIdentity(file, version, syntheticCommit)
		closeErr := file.Close()
		if !matches || closeErr != nil {
			t.Fatalf("fixture stamp mismatch: version=%s close=%v", version, closeErr)
		}
	}
	first[0] ^= 0xff
	if data := buildUpdateFixture(t, t.TempDir(), "1.0.1"); data[0] == first[0] {
		t.Fatal("mutating returned bytes changed the cached fixture")
	}
}

type syntheticDelivery struct {
	selected                                   releaseSelection
	content                                    []byte
	selectCalls, prepareCalls, validationCalls int
	prepareError, validationError              error
	verifierError                              error
	verifierCalls                              int
	beforePrepare, beforeValidation            func()
}

func (d *syntheticDelivery) checkVerifier(context.Context, *noticeCache) error {
	d.verifierCalls++
	return d.verifierError
}

func (d *syntheticDelivery) selectRelease(context.Context, string) (releaseSelection, error) {
	d.selectCalls++
	return d.selected, nil
}
func (d *syntheticDelivery) prepare(ctx context.Context, cache *noticeCache, selected releaseSelection) (string, error) {
	d.prepareCalls++
	if d.beforePrepare != nil {
		d.beforePrepare()
	}
	if d.prepareError != nil {
		return "", d.prepareError
	}
	f, err := cache.root.OpenFile("next", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0700)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.Write(d.content); err != nil {
		return "", err
	}
	return fileDigest(ctx, f)
}
func (d *syntheticDelivery) stillPublished(context.Context, releaseSelection) error {
	d.validationCalls++
	if d.beforeValidation != nil {
		d.beforeValidation()
	}
	return d.validationError
}

func updateTestInstallation(t *testing.T, content []byte) *updateInstallation {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	directory = filepath.Join(directory, ".openai", "bin")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "openai")
	if err := os.WriteFile(path, content, 0700); err != nil {
		t.Fatal(err)
	}
	i, err := openInstallation(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(i.close)
	return i
}

func TestAutomaticUpdateTransactions(t *testing.T) {
	t.Setenv("OPENAI_CLI_NO_AUTO_UPDATE", "")
	t.Setenv("OPENAI_CLI_NO_UPDATE_CHECK", "")
	t.Setenv("OPENAI_CLI_VERSION_PIN", "")
	next := buildUpdateFixture(t, t.TempDir(), "1.0.1")
	old := []byte("original installation")
	for _, scenario := range []string{"activate", "withdrawn", "pin_conflict", "opt_out", "replaced_binary", "inplace_binary", "replaced_state", "inplace_state", "replaced_lock", "replaced_next", "inplace_next", "symlink_next", "fifo_next", "canceled", "rename_failure"} {
		t.Run(scenario, func(t *testing.T) {
			i := updateTestInstallation(t, old)
			delivery := &syntheticDelivery{selected: syntheticSelection(), content: next}
			worker := updateWorker{installation: i, current: "1.0.0", now: time.Now(), delivery: delivery}
			if err := worker.run(context.Background()); err != nil {
				t.Fatal(err)
			}
			current, err := os.ReadFile(i.path)
			if err != nil || !bytes.Equal(current, old) {
				t.Fatal("preparation changed installed binary")
			}
			if delivery.prepareCalls != 1 || delivery.validationCalls != 0 {
				t.Fatal("preparation activated an update")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			worker.now = worker.now.Add(time.Second)
			statePath := filepath.Join(i.stateDirectory(), stateName)
			nextPath := filepath.Join(i.stateDirectory(), "next")
			delivery.beforeValidation = func() {
				switch scenario {
				case "withdrawn":
					delivery.validationError = errUpdate
				case "pin_conflict":
					t.Setenv("OPENAI_CLI_VERSION_PIN", "1.0.0")
				case "opt_out":
					t.Setenv("OPENAI_CLI_NO_AUTO_UPDATE", "1")
				case "replaced_binary":
					if err := os.Rename(i.path, i.path+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(i.path, old, 0700); err != nil {
						t.Fatal(err)
					}
				case "inplace_binary":
					if err := os.WriteFile(i.path, []byte("manual installation"), 0700); err != nil {
						t.Fatal(err)
					}
				case "replaced_state":
					if err := os.Rename(statePath, statePath+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(statePath, []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
				case "inplace_state":
					if err := os.WriteFile(statePath, []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
				case "replaced_lock":
					p := filepath.Join(i.stateDirectory(), ".lock")
					if err := os.Rename(p, p+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(p, nil, 0600); err != nil {
						t.Fatal(err)
					}
				case "replaced_next":
					if err := os.Remove(nextPath); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(nextPath, []byte("wrong"), 0700); err != nil {
						t.Fatal(err)
					}
				case "inplace_next":
					if err := os.WriteFile(nextPath, []byte("wrong"), 0700); err != nil {
						t.Fatal(err)
					}
				case "symlink_next":
					if err := os.Remove(nextPath); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(i.path, nextPath); err != nil {
						t.Fatal(err)
					}
				case "fifo_next":
					if err := os.Remove(nextPath); err != nil {
						t.Fatal(err)
					}
					if err := syscall.Mkfifo(nextPath, 0600); err != nil {
						t.Fatal(err)
					}
				case "canceled":
					cancel()
				case "rename_failure":
					if os.Geteuid() == 0 {
						t.Skip("root bypasses directory permissions")
					}
					if err := os.Chmod(filepath.Dir(i.path), 0500); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { os.Chmod(filepath.Dir(i.path), 0700) })
				}
			}
			err = worker.run(ctx)
			current, readErr := os.ReadFile(i.path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if scenario == "activate" {
				if err != nil || !bytes.Equal(current, next) {
					t.Fatalf("activation failed: %v", err)
				}
				// An already opened executable keeps its original bytes after rename.
				before := make([]byte, len(old))
				if _, err := i.file.ReadAt(before, 0); err != nil || !bytes.Equal(before, old) {
					t.Fatal("replaced running inode")
				}
				data, err := os.ReadFile(statePath)
				if err != nil {
					t.Fatal(err)
				}
				var state updateState
				if json.Unmarshal(data, &state) != nil || state.Applied != "1.0.1" || state.Announced {
					t.Fatal("invalid receipt")
				}
			} else {
				if err == nil {
					t.Fatal("failure accepted")
				}
				want := old
				if scenario == "inplace_binary" {
					want = []byte("manual installation")
				}
				if !bytes.Equal(current, want) {
					t.Fatal("failed update replaced working installation")
				}
			}
		})
	}
}

func TestAutomaticUpdateDiscoveryThrottleAndLock(t *testing.T) {
	t.Setenv("OPENAI_CLI_NO_AUTO_UPDATE", "")
	t.Setenv("OPENAI_CLI_NO_UPDATE_CHECK", "")
	t.Setenv("OPENAI_CLI_VERSION_PIN", "")
	i := updateTestInstallation(t, []byte("old"))
	d := &syntheticDelivery{selected: syntheticSelection(), prepareError: errors.New("interrupted download")}
	w := updateWorker{installation: i, current: "1.0.0", now: time.Now(), delivery: d}
	entered, release := make(chan struct{}), make(chan struct{})
	d.beforePrepare = func() { close(entered); <-release }
	finished := make(chan error, 1)
	go func() { finished <- w.run(context.Background()) }()
	<-entered
	if err := w.run(context.Background()); err == nil {
		t.Fatal("second worker acquired active lock")
	}
	close(release)
	if err := <-finished; err == nil {
		t.Fatal("interrupted preparation succeeded")
	}
	d.beforePrepare = nil
	w.now = w.now.Add(automaticInterval - time.Second)
	if err := w.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d.selectCalls != 1 {
		t.Fatal("retried before four hours")
	}
	w.now = w.now.Add(time.Second)
	if err := w.run(context.Background()); err == nil {
		t.Fatal("expected synthetic download error")
	}
	if d.selectCalls != 2 {
		t.Fatal("did not retry at four hours")
	}
}

func TestAutomaticUpdateInstallationOwnership(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "shared_file", "shared_directory", "directory_symlink"} {
		t.Run(kind, func(t *testing.T) {
			i := updateTestInstallation(t, []byte("old"))
			i.close()
			switch kind {
			case "symlink":
				os.Rename(i.path, i.path+".target")
				os.Symlink(i.path+".target", i.path)
			case "hardlink":
				os.Link(i.path, i.path+".link")
			case "shared_file":
				os.Chmod(i.path, 0777)
			case "shared_directory":
				os.Chmod(filepath.Dir(i.path), 0777)
			case "directory_symlink":
				directory := filepath.Dir(i.path)
				os.Rename(directory, directory+".old")
				os.Symlink(directory+".old", directory)
			}
			opened, err := openInstallation(i.path)
			if err == nil {
				opened.close()
				t.Fatal("unsupported installation admitted")
			}
		})
	}
}

func TestAutomaticUpdateVerifierFallbackRecovery(t *testing.T) {
	t.Setenv("OPENAI_CLI_NO_AUTO_UPDATE", "")
	t.Setenv("OPENAI_CLI_NO_UPDATE_CHECK", "")
	t.Setenv("OPENAI_CLI_VERSION_PIN", "")
	i := updateTestInstallation(t, []byte("old"))
	d := &syntheticDelivery{selected: syntheticSelection(), verifierError: errUpdate, prepareError: errUpdate}
	w := updateWorker{installation: i, current: "1.0.0", now: time.Now(), delivery: d}
	if err := w.run(t.Context()); err == nil {
		t.Fatal("unsupported verifier accepted")
	}
	readState := func() updateState {
		t.Helper()
		cache, err := openCache(t.Context(), i.stateDirectory())
		if err != nil {
			t.Fatal(err)
		}
		defer cache.close()
		state, err := loadUpdateState(cache, w.now)
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	if state := readState(); !state.VerifierUnavailable || state.Checked != w.now.Unix() || state.Pending != nil {
		t.Fatalf("unsupported verifier lost fallback or throttle: %+v", state)
	}
	if d.selectCalls != 0 || d.prepareCalls != 0 {
		t.Fatal("unsupported verifier triggered release requests")
	}
	w.now = w.now.Add(automaticInterval - time.Second)
	if err := w.run(t.Context()); err != nil || d.verifierCalls != 1 {
		t.Fatal("retried capabilities before four hours")
	}
	d.verifierError = nil
	w.now = w.now.Add(time.Second)
	if err := w.run(t.Context()); err == nil {
		t.Fatal("expected synthetic preparation failure")
	}
	if readState().VerifierUnavailable || d.verifierCalls != 2 || d.selectCalls != 1 || d.prepareCalls != 1 {
		t.Fatal("compatible verifier did not restore update preparation")
	}
	content, err := os.ReadFile(i.path)
	if err != nil || string(content) != "old" {
		t.Fatal("capability probes changed installation")
	}
}

func TestAutomaticUpdatePolicy(t *testing.T) {
	for _, value := range []string{"latest", "1.2", "1.2.3-rc1", "01.2.3", "1.2.3+build", " 1.2.3"} {
		_, enabled := updatePolicy(func(key string) string {
			if key == "OPENAI_CLI_VERSION_PIN" {
				return value
			}
			return ""
		})
		if enabled {
			t.Errorf("invalid pin admitted: %q", value)
		}
	}
	for _, key := range []string{"OPENAI_CLI_NO_AUTO_UPDATE", "OPENAI_CLI_NO_UPDATE_CHECK"} {
		_, enabled := updatePolicy(func(k string) string {
			if k == key {
				return "TRUE"
			}
			return ""
		})
		if enabled {
			t.Fatal(key)
		}
	}
	if !allowedVersion("1.0.0", "1.0.1", "1.0.0") || allowedVersion("1.0.0", "1.0.1", "") || allowedVersion("1.0.2", "1.0.1", "1.0.1") {
		t.Fatal("pin contract violated")
	}
}

func TestAutomaticUpdateRecoversStalePreparation(t *testing.T) {
	t.Setenv("OPENAI_CLI_NO_AUTO_UPDATE", "")
	t.Setenv("OPENAI_CLI_NO_UPDATE_CHECK", "")
	t.Setenv("OPENAI_CLI_VERSION_PIN", "")
	next := buildUpdateFixture(t, t.TempDir(), "1.0.1")
	for _, scenario := range []string{"missing", "corrupt", "manual_replacement", "withdrawn"} {
		t.Run(scenario, func(t *testing.T) {
			i := updateTestInstallation(t, []byte("old"))
			d := &syntheticDelivery{selected: syntheticSelection(), content: next}
			worker := updateWorker{installation: i, current: "1.0.0", now: time.Now(), delivery: d}
			if err := worker.run(t.Context()); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing":
				if err := os.Remove(filepath.Join(i.stateDirectory(), "next")); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(filepath.Join(i.stateDirectory(), "next"), []byte("bad"), 0700); err != nil {
					t.Fatal(err)
				}
			case "manual_replacement":
				if err := os.WriteFile(i.path, []byte("manual repair"), 0700); err != nil {
					t.Fatal(err)
				}
				i.close()
				var err error
				i, err = openInstallation(i.path)
				if err != nil {
					t.Fatal(err)
				}
				defer i.close()
				worker.installation = i
			case "withdrawn":
				d.validationError = errWithdrawn
			}
			worker.now = worker.now.Add(time.Second)
			if err := worker.run(t.Context()); err == nil {
				t.Fatal("stale preparation activated")
			}
			d.validationError = nil
			worker.now = worker.now.Add(automaticInterval)
			if err := worker.run(t.Context()); err != nil {
				t.Fatal(err)
			}
			if d.prepareCalls != 2 || d.selectCalls != 2 {
				t.Fatalf("stale update blocked discovery: %+v", d)
			}
			worker.now = worker.now.Add(time.Second)
			if err := worker.run(t.Context()); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(i.path)
			if err != nil || !bytes.Equal(content, next) {
				t.Fatal("recovered update did not activate")
			}
		})
	}
}
