package updatenotice

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCacheRejectsReplacedLockAndDirectory(t *testing.T) {
	for _, replace := range []string{"lock", "directory"} {
		t.Run(replace, func(t *testing.T) {
			directory := filepath.Join(t.TempDir(), "notice")
			cache, err := openCache(context.Background(), directory)
			if err != nil {
				t.Fatal(err)
			}
			defer cache.close()
			if replace == "lock" {
				if err := os.Rename(filepath.Join(directory, ".lock"), filepath.Join(directory, ".old-lock")); err != nil {
					t.Fatal(err)
				}
				fixtureFile(t, filepath.Join(directory, ".lock"), "")
			} else {
				if err := os.Rename(directory, directory+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := cache.save(cacheState{Schema: 1, Checked: time.Now().Unix()}); err == nil {
				t.Fatal("replaced state accepted a write")
			}
		})
	}
}

func TestCachePrivateFilesAndTemporaryCleanup(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "notice")
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return response(releaseBody("v2.0.0")), nil })}
	if got := check(context.Background(), "1.0.0", directory, time.Now(), client, unknownInstallation); got == "" {
		t.Fatal("no notice")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("cache entries = %d", len(entries))
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			t.Fatal("nonregular cache entry")
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
			t.Fatalf("mode = %v", info.Mode())
		}
	}
}

func TestCacheRefusesSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks can require Windows privileges")
	}
	for _, name := range []string{stateName, ".lock"} {
		t.Run(name, func(t *testing.T) {
			directory := privateCacheDirectory(t)
			target := filepath.Join(t.TempDir(), "original")
			fixtureFile(t, target, "keep original")
			if name == ".lock" {
				if err := os.Remove(filepath.Join(directory, name)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("unexpected request")
				return response(releaseBody("v2.0.0")), nil
			})}
			if got := check(context.Background(), "1.0.0", directory, time.Now(), client, unknownInstallation); got != "" {
				t.Fatalf("symlink notice = %q", got)
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "keep original" {
				t.Fatal("symlink target changed")
			}
		})
	}
}

func TestCachedNoticePreservesExternalStateChanges(t *testing.T) {
	for _, replace := range []bool{true, false} {
		t.Run(map[bool]string{true: "replacement", false: "same inode edit"}[replace], func(t *testing.T) {
			directory := privateCacheDirectory(t)
			now := time.Now()
			path := filepath.Join(directory, stateName)
			cache, err := openCache(context.Background(), directory)
			if err != nil {
				t.Fatal(err)
			}
			if err := cache.save(cacheState{Schema: 1, Checked: now.Unix(), Latest: "2.0.0"}); err != nil {
				cache.close()
				t.Fatal(err)
			}
			cache.close()
			called := false
			guidance := func(string) string {
				called = true
				if replace {
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
				}
				fixtureFile(t, path, "external replacement")
				return manualGuidance
			}
			if got := check(context.Background(), "1.0.0", directory, now, nil, guidance); got != "" {
				t.Fatalf("changed-cache notice = %q", got)
			}
			if !called {
				t.Fatal("cached notice never reached the external mutation")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "external replacement" {
				t.Fatal("external cache change was overwritten")
			}
		})
	}
}
