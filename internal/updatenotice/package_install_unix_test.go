//go:build darwin || linux

package updatenotice

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPackageEnrollmentTransitions(t *testing.T) {
	if !automaticPlatform() {
		t.Skip("package setup requires an ordinary user")
	}
	a, b := "1.0.0", "1.0.1"
	fixtures := map[string][]byte{
		a: buildUpdateFixture(t, t.TempDir(), a),
		b: buildUpdateFixture(t, t.TempDir(), b),
	}
	for _, scenario := range []string{"system package upgraded before discovery", "same package restores its marking", "automatic upgrade before repeat", "interrupted explicit upgrade", "competing worker", "unknown private binary", "unregistered release stamp"} {
		t.Run(scenario, func(t *testing.T) {
			home, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HOME", home)
			path := filepath.Join(home, ".openai", "bin")
			if err := os.MkdirAll(path, 0700); err != nil {
				t.Fatal(err)
			}
			bin, err := os.OpenRoot(path)
			if err != nil {
				t.Fatal(err)
			}
			defer bin.Close()
			markIncoming := false
			install := func(version string) (string, error) {
				t.Helper()
				dir, err := os.MkdirTemp(path, ".openai-install.")
				if err != nil {
					t.Fatal(err)
				}
				source := filepath.Join(dir, "openai")
				if err := os.WriteFile(source, fixtures[version], 0700); err != nil {
					t.Fatal(err)
				}
				if markIncoming {
					markUpdateFixture(t, source)
				}
				staged, err := openInstallation(source)
				if err != nil {
					t.Fatal(err)
				}
				defer staged.close()
				return installPackageRelease(t.Context(), bin, staged, version, syntheticCommit)
			}
			requireVersion := func(system, expected string) {
				t.Helper()
				got, err := install(system)
				if err != nil || got != expected {
					t.Fatalf("enroll system %s: got %q, want %q: %v", system, got, expected, err)
				}
				got, err = packageInstalledVersion(t.Context(), bin)
				data, readErr := os.ReadFile(filepath.Join(path, "openai"))
				if err != nil || got != expected || readErr != nil || !bytes.Equal(data, fixtures[expected]) {
					t.Fatalf("active bytes do not match %s: %s %v %v", expected, got, err, readErr)
				}
			}
			if scenario == "unknown private binary" || scenario == "unregistered release stamp" {
				existing := []byte("personal executable")
				if scenario == "unregistered release stamp" {
					existing = fixtures[b]
				}
				if err := os.WriteFile(filepath.Join(path, "openai"), existing, 0700); err != nil {
					t.Fatal(err)
				}
				if _, err := install(a); err == nil {
					t.Fatal("accepted unknown private bytes")
				}
				data, err := os.ReadFile(filepath.Join(path, "openai"))
				if err != nil || !bytes.Equal(data, existing) {
					t.Fatal("changed unrelated executable")
				}
				return
			}
			requireVersion(a, a)
			if scenario == "same package restores its marking" {
				markIncoming = true
				requireVersion(a, a)
				checkUpdateFixtureQuarantine(t, filepath.Join(path, "openai"))
				return
			}
			if scenario == "system package upgraded before discovery" {
				markUpdateFixture(t, filepath.Join(path, "openai"))
			}
			if scenario == "interrupted explicit upgrade" || scenario == "competing worker" {
				cache, err := openCache(t.Context(), filepath.Join(path, ".update"))
				if err != nil {
					t.Fatal(err)
				}
				state, err := loadUpdateState(cache, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "interrupted explicit upgrade" {
					// The process stopped after saving its intended identity, before rename.
					nextHash := sha256.Sum256(fixtures[b])
					state.PackageSources = append([]packageIdentity{{b, syntheticCommit, hex.EncodeToString(nextHash[:])}}, state.PackageSources...)
					if err := cache.saveState(state); err != nil {
						t.Fatal(err)
					}
				} else {
					released := time.AfterFunc(50*time.Millisecond, cache.close)
					defer released.Stop()
					requireVersion(b, b)
					requireVersion(a, b)
					return
				}
				cache.close()
				if data, err := os.ReadFile(filepath.Join(path, "openai")); err != nil || !bytes.Equal(data, fixtures[a]) {
					t.Fatal("changed the initial executable before explicit commit")
				}
			}
			if scenario == "automatic upgrade before repeat" {
				current, err := openInstallation(filepath.Join(path, "openai"))
				if err != nil {
					t.Fatal(err)
				}
				defer current.close()
				delivery := &syntheticDelivery{selected: syntheticSelection(), content: fixtures[b]}
				worker := updateWorker{installation: current, current: a, now: time.Now(), delivery: delivery}
				if err := worker.run(t.Context()); err != nil {
					t.Fatal("prepare package user's first automatic upgrade:", err)
				}
				if err := worker.run(t.Context()); err != nil {
					t.Fatal("activate package user's first automatic upgrade:", err)
				}
			} else {
				requireVersion(b, b)
				cache, err := openCache(t.Context(), filepath.Join(path, ".update"))
				if err != nil {
					t.Fatal(err)
				}
				state, err := loadUpdateState(cache, time.Now())
				cache.close()
				if err != nil || state.Applied != "" || state.Pending != nil {
					t.Fatal("explicit installation created automatic-update confirmation")
				}
			}
			requireVersion(a, b)
			requireVersion(b, b)
			if scenario == "system package upgraded before discovery" {
				checkUpdateFixtureQuarantine(t, filepath.Join(path, "openai"))
			}
		})
	}
}
