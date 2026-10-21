package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const emptyProfileJSON = `{"version":1,"default_profile":null,"profiles":{}}`

func profileRegistryTestPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "openai", "profiles.json")
}

func writeProfileRegistryTestFile(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
	require.NoError(t, os.WriteFile(path, []byte(data), 0600))
}

func profileStoreString(value string) *string { return &value }

func TestProfileStorePathAndMissingRegistry(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("HOME", directory)
	t.Setenv("XDG_CONFIG_HOME", directory)
	t.Setenv("AppData", directory)
	config, err := os.UserConfigDir()
	require.NoError(t, err)
	path, err := profileStorePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(config, "openai", "profiles.json"), path)
	registry, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, emptyProfileRegistry(), registry)
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestProfileStorePreservesPresenceAndExplicitEdits(t *testing.T) {
	path := profileRegistryTestPath(t)
	t.Setenv("OPENAI_ORG_ID", "org_ambient")
	t.Setenv("OPENAI_PROJECT_ID", "proj_ambient")
	t.Setenv("OPENAI_BASE_URL", "https://ambient.invalid")
	t.Setenv("OPENAI_PROFILE", "ambient")
	t.Setenv("OPENAI_API_KEY", "sk_fake_ambient")
	registry, err := updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles["Work"] = profileValues{}
		registry.Profiles["work"] = profileValues{Project: profileStoreString("custom/project")}
		registry.Profiles["研发 team"] = profileValues{
			Organization: profileStoreString(""), Project: profileStoreString(""), BaseURL: profileStoreString(""),
		}
		registry.DefaultProfile = profileStoreString("work")
		return nil
	})
	require.NoError(t, err)
	loaded, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, registry, loaded)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var encoded map[string]any
	require.NoError(t, json.Unmarshal(data, &encoded))
	profiles := encoded["profiles"].(map[string]any)
	require.Empty(t, profiles["Work"])
	require.Equal(t, map[string]any{"project": "custom/project"}, profiles["work"])
	require.Equal(t, map[string]any{"organization": "", "project": "", "base_url": ""}, profiles["研发 team"])
	require.NotContains(t, string(data), "ambient")
	registry, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		values := registry.Profiles["研发 team"]
		values.Project = nil
		registry.Profiles["研发 team"] = values
		return nil
	})
	require.NoError(t, err)
	require.Nil(t, registry.Profiles["研发 team"].Project)
	require.Equal(t, "custom/project", *registry.Profiles["work"].Project)
	files, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, files, 2, "only profiles and the persistent lock remain")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
		info, err = os.Stat(filepath.Dir(path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0700), info.Mode().Perm())
	}
}

func TestProfileStoreRejectsMalformedWithoutOverwriting(t *testing.T) {
	for name, data := range map[string]string{
		"empty": "", "null": "null", "array": "[]", "empty-object": "{}", "trailing": emptyProfileJSON + " {}",
		"missing-version":      `{"default_profile":null,"profiles":{}}`,
		"missing-default":      `{"version":1,"profiles":{}}`,
		"missing-profiles":     `{"version":1,"default_profile":null}`,
		"future-version":       `{"version":2,"default_profile":null,"profiles":{}}`,
		"null-version":         `{"version":null,"default_profile":null,"profiles":{}}`,
		"string-version":       `{"version":"1","default_profile":null,"profiles":{}}`,
		"fraction-version":     `{"version":1.0,"default_profile":null,"profiles":{}}`,
		"null-profiles":        `{"version":1,"default_profile":null,"profiles":null}`,
		"array-profiles":       `{"version":1,"default_profile":null,"profiles":[]}`,
		"array-value":          `{"version":1,"default_profile":null,"profiles":{"work":[]}}`,
		"null-value":           `{"version":1,"default_profile":null,"profiles":{"work":null}}`,
		"null-project":         `{"version":1,"default_profile":null,"profiles":{"work":{"project":null}}}`,
		"null-organization":    `{"version":1,"default_profile":null,"profiles":{"work":{"organization":null}}}`,
		"null-url":             `{"version":1,"default_profile":null,"profiles":{"work":{"base_url":null}}}`,
		"number-project":       `{"version":1,"default_profile":null,"profiles":{"work":{"project":1}}}`,
		"array-organization":   `{"version":1,"default_profile":null,"profiles":{"work":{"organization":[]}}}`,
		"boolean-url":          `{"version":1,"default_profile":null,"profiles":{"work":{"base_url":true}}}`,
		"unknown-top-field":    `{"version":1,"default_profile":null,"profiles":{},"extra":true}`,
		"credential-value":     `{"version":1,"default_profile":null,"profiles":{"work":{"api_key":"sk_fake"}}}`,
		"credential-reference": `{"version":1,"default_profile":null,"profiles":{"work":{"credential_ref":"fake"}}}`,
		"duplicate-version":    `{"version":1,"version":1,"default_profile":null,"profiles":{}}`,
		"duplicate-default":    `{"version":1,"default_profile":null,"default_profile":null,"profiles":{}}`,
		"duplicate-profiles":   `{"version":1,"default_profile":null,"profiles":{},"profiles":{}}`,
		"duplicate-profile":    `{"version":1,"default_profile":null,"profiles":{"work":{},"wo\u0072k":{}}}`,
		"duplicate-value":      `{"version":1,"default_profile":null,"profiles":{"work":{"project":"one","project":"two"}}}`,
		"dangling-default":     `{"version":1,"default_profile":"missing","profiles":{"work":{}}}`,
		"empty-default":        `{"version":1,"default_profile":"","profiles":{}}`,
		"numeric-default":      `{"version":1,"default_profile":1,"profiles":{}}`,
		"empty-name":           `{"version":1,"default_profile":null,"profiles":{"":{}}}`,
		"whitespace-name":      `{"version":1,"default_profile":null,"profiles":{"work ":{}}}`,
		"control-name":         `{"version":1,"default_profile":null,"profiles":{"wo\u001brk":{}}}`,
		"control-value":        `{"version":1,"default_profile":null,"profiles":{"work":{"project":"a\r\nb"}}}`,
		"url-userinfo":         `{"version":1,"default_profile":null,"profiles":{"work":{"base_url":"https://fake:fake@example.invalid"}}}`,
		"url-query":            `{"version":1,"default_profile":null,"profiles":{"work":{"base_url":"https://example.invalid?"}}}`,
		"url-fragment":         `{"version":1,"default_profile":null,"profiles":{"work":{"base_url":"https://example.invalid#"}}}`,
		"invalid-utf8":         "{\"version\":1,\"default_profile\":null,\"profiles\":{\"\xff\":{}}}",
	} {
		t.Run(name, func(t *testing.T) {
			path := profileRegistryTestPath(t)
			writeProfileRegistryTestFile(t, path, data)
			_, err := loadProfiles(t.Context(), path)
			require.ErrorIs(t, err, errProfilesInvalid)
			called := false
			_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				called = true
				registry.Profiles["new"] = profileValues{}
				return nil
			})
			require.ErrorIs(t, err, errProfilesInvalid)
			require.False(t, called, "malformed bytes must not reach the editor")
			actual, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, string(actual))
		})
	}
}

func TestProfileStoreValidatesNamesAndEndpoints(t *testing.T) {
	for _, name := range []string{"work", "Work", "研发 team", "../work", "a/b", "with interior space", "�"} {
		require.True(t, validProfileName(name), "%q", name)
	}
	for _, name := range []string{"", " work", "work\u00a0", "a\x00b", "a\x1bb", "a\u0085b", "\xff"} {
		require.False(t, validProfileName(name), "%q", name)
	}
	for _, endpoint := range []string{"", "https://example.invalid", "http://127.0.0.1:4000/v1/", "https://[::1]/v1", "https://example.invalid/a%3Fb"} {
		require.True(t, validProfileBaseURL(endpoint), "%q", endpoint)
	}
	for _, endpoint := range []string{"relative", "/v1", "https:", "https:///v1", "ftp://example.invalid", "https://u@example.invalid", "https://example.invalid?key=x", "https://example.invalid?", "https://example.invalid#x", "https://example.invalid#", "https://example.invalid/\n", "https://example.invalid/\xff"} {
		require.False(t, validProfileBaseURL(endpoint), "%q", endpoint)
	}
}

func TestProfileStoreInvalidEditsPreserveBytes(t *testing.T) {
	for name, edit := range map[string]func(*profileRegistry){
		"version":          func(registry *profileRegistry) { registry.Version = 2 },
		"nil-profiles":     func(registry *profileRegistry) { registry.Profiles = nil },
		"dangling-default": func(registry *profileRegistry) { registry.DefaultProfile = profileStoreString("missing") },
		"empty-name":       func(registry *profileRegistry) { registry.Profiles[""] = profileValues{} },
		"invalid-name":     func(registry *profileRegistry) { registry.Profiles["\xff"] = profileValues{} },
		"organization-control": func(registry *profileRegistry) {
			registry.Profiles["work"] = profileValues{Organization: profileStoreString("a\tb")}
		},
		"project-control": func(registry *profileRegistry) {
			registry.Profiles["work"] = profileValues{Project: profileStoreString("a\x7fb")}
		},
		"url": func(registry *profileRegistry) {
			registry.Profiles["work"] = profileValues{BaseURL: profileStoreString("https://u@example.invalid")}
		},
	} {
		t.Run(name, func(t *testing.T) {
			path := profileRegistryTestPath(t)
			before := emptyProfileJSON + " \n"
			writeProfileRegistryTestFile(t, path, before)
			_, err := updateProfiles(t.Context(), path, func(registry *profileRegistry) error { edit(registry); return nil })
			require.ErrorIs(t, err, errProfilesInvalid)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, string(data))
		})
	}
	path := profileRegistryTestPath(t)
	writeProfileRegistryTestFile(t, path, emptyProfileJSON)
	editError := errors.New("edit rejected")
	_, err := updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles["discard"] = profileValues{}
		return editError
	})
	require.ErrorIs(t, err, editError)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, emptyProfileJSON, string(data))
}

func TestProfileStoreLocalSizeBoundary(t *testing.T) {
	path := profileRegistryTestPath(t)
	before := emptyProfileJSON + strings.Repeat(" ", maxProfileBytes-len(emptyProfileJSON))
	writeProfileRegistryTestFile(t, path, before)
	_, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles["large"] = profileValues{Project: profileStoreString(strings.Repeat("p", maxProfileBytes))}
		return nil
	})
	require.ErrorIs(t, err, errProfilesTooLarge)
	require.ErrorContains(t, err, "1 MiB local registry limit")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, string(data))
	writeProfileRegistryTestFile(t, path, before+" ")
	_, err = loadProfiles(t.Context(), path)
	require.ErrorIs(t, err, errProfilesTooLarge)
}

func TestProfileStoreConcurrentWritersPreserveProfiles(t *testing.T) {
	path := profileRegistryTestPath(t)
	const writers = 12
	start := make(chan struct{})
	results := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			<-start
			_, err := updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				registry.Profiles[fmt.Sprintf("work-%d", i)] = profileValues{Project: profileStoreString(fmt.Sprint(i))}
				return nil
			})
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	registry, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Len(t, registry.Profiles, writers)
	for i := range writers {
		require.Equal(t, fmt.Sprint(i), *registry.Profiles[fmt.Sprintf("work-%d", i)].Project)
	}
}

func TestProfileStoreWaitingWriterResolvesCurrentDefault(t *testing.T) {
	path := profileRegistryTestPath(t)
	writeProfileRegistryTestFile(t, path, `{"version":1,"default_profile":"one","profiles":{"one":{},"two":{}}}`)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	inside := make(chan struct{})
	release := make(chan struct{})
	first := make(chan error, 1)
	go func() {
		_, err := updateProfiles(ctx, path, func(registry *profileRegistry) error {
			close(inside)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			registry.DefaultProfile = profileStoreString("two")
			registry.Profiles["one"] = profileValues{Organization: profileStoreString("keep")}
			return nil
		})
		first <- err
	}()
	select {
	case <-inside:
	case <-ctx.Done():
		t.Fatal("first writer did not enter its transaction")
	}
	observed := &observedProjectLinkWait{Context: ctx, waiting: make(chan struct{})}
	second := make(chan error, 1)
	go func() {
		_, err := updateProfiles(observed, path, func(registry *profileRegistry) error {
			values := registry.Profiles[*registry.DefaultProfile]
			values.Project = profileStoreString("switched")
			registry.Profiles[*registry.DefaultProfile] = values
			return nil
		})
		second <- err
	}()
	select {
	case <-observed.waiting:
	case err := <-second:
		t.Fatalf("second writer did not wait: %v", err)
	case <-ctx.Done():
		t.Fatal("second writer did not reach lock wait")
	}
	close(release)
	require.NoError(t, <-first)
	require.NoError(t, <-second)
	registry, err := loadProfiles(t.Context(), path)
	require.NoError(t, err)
	require.Equal(t, "two", *registry.DefaultProfile)
	require.Equal(t, "switched", *registry.Profiles["two"].Project)
	require.Nil(t, registry.Profiles["one"].Project)
	require.Equal(t, "keep", *registry.Profiles["one"].Organization)
}

func TestProfileStoreCancellationPreservesStateAndReleasesLock(t *testing.T) {
	path := profileRegistryTestPath(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := loadProfiles(ctx, path)
	require.ErrorIs(t, err, context.Canceled)
	_, err = updateProfiles(ctx, path, func(*profileRegistry) error { t.Fatal("editor called"); return nil })
	require.ErrorIs(t, err, context.Canceled)
	_, err = os.Stat(filepath.Dir(path))
	require.ErrorIs(t, err, os.ErrNotExist)
	writeProfileRegistryTestFile(t, path, emptyProfileJSON)
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	_, err = updateProfiles(ctx, path, func(registry *profileRegistry) error {
		registry.Profiles["discard"] = profileValues{}
		cancel()
		return nil
	})
	require.ErrorIs(t, err, context.Canceled)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, emptyProfileJSON, string(data))
	_, err = updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
		registry.Profiles["after"] = profileValues{}
		return nil
	})
	require.NoError(t, err)
	files, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	require.Len(t, files, 2)
}

func TestProfileStoreCancellationWhileWaiting(t *testing.T) {
	path := profileRegistryTestPath(t)
	root, name, err := openProjectLinkDirectory(path, true)
	require.NoError(t, err)
	defer root.Close()
	lock, err := lockProjectLinks(t.Context(), root, name)
	require.NoError(t, err)
	defer lock.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err = updateProfiles(ctx, path, func(*profileRegistry) error { t.Fatal("editor called"); return nil })
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = os.Stat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NoError(t, lock.Close())
	_, err = updateProfiles(t.Context(), path, func(*profileRegistry) error { return nil })
	require.NoError(t, err)
}

func TestProfileStoreSnapshotRejectsReplacementAndRewrite(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		t.Run(fmt.Sprint(replacement), func(t *testing.T) {
			path := profileRegistryTestPath(t)
			writeProfileRegistryTestFile(t, path, emptyProfileJSON)
			root, name, err := openProjectLinkDirectory(path, false)
			require.NoError(t, err)
			defer root.Close()
			info, err := root.Lstat(name)
			require.NoError(t, err)
			if replacement {
				temporary := filepath.Join(filepath.Dir(path), "replacement")
				writeProfileRegistryTestFile(t, temporary, emptyProfileJSON)
				require.NoError(t, os.Rename(temporary, path))
			} else {
				writeProfileRegistryTestFile(t, path, emptyProfileJSON+" ")
			}
			_, err = readProfileSnapshot(t.Context(), root, name, info)
			require.ErrorIs(t, err, errProfilesChanged)
		})
	}
}

func TestProfileStoreRejectsReplacementBeforeCommit(t *testing.T) {
	for _, kind := range []string{"registry", "registry-symlink", "rewrite", "lock", "directory", "new-registry"} {
		t.Run(kind, func(t *testing.T) {
			path := profileRegistryTestPath(t)
			if kind != "new-registry" {
				writeProfileRegistryTestFile(t, path, emptyProfileJSON)
			}
			before := emptyProfileJSON + " \n"
			if kind == "registry-symlink" {
				target := filepath.Join(t.TempDir(), "target")
				writeProfileRegistryTestFile(t, target, before)
				if err := os.Symlink(target, path+"-replacement"); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			_, err := updateProfiles(t.Context(), path, func(registry *profileRegistry) error {
				registry.Profiles["discard"] = profileValues{}
				switch kind {
				case "registry-symlink":
					require.NoError(t, os.Rename(path+"-replacement", path))
				case "directory":
					require.NoError(t, os.Rename(filepath.Dir(path), filepath.Dir(path)+"-old"))
					writeProfileRegistryTestFile(t, path, before)
				case "lock":
					lockPath := filepath.Join(filepath.Dir(path), ".profiles.json.lock")
					require.NoError(t, os.Rename(lockPath, lockPath+"-old"))
					require.NoError(t, os.WriteFile(lockPath, nil, 0600))
					before = emptyProfileJSON
				case "rewrite", "new-registry":
					writeProfileRegistryTestFile(t, path, before)
				default:
					temporary := filepath.Join(filepath.Dir(path), "replacement")
					writeProfileRegistryTestFile(t, temporary, before)
					require.NoError(t, os.Rename(temporary, path))
				}
				return nil
			})
			require.ErrorIs(t, err, errProfilesChanged)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, string(data))
			if kind == "registry-symlink" {
				info, err := os.Lstat(path)
				require.NoError(t, err)
				require.NotZero(t, info.Mode()&os.ModeSymlink)
			}
		})
	}
}

func TestProfileStoreMissingRegistryPreservesConfiguration(t *testing.T) {
	for _, kind := range []string{"symlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			path := profileRegistryTestPath(t)
			if kind == "symlink" {
				if err := os.Symlink(t.TempDir(), filepath.Dir(path)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				if runtime.GOOS == "windows" {
					t.Skip("Unix permission bits do not verify Windows ACLs")
				}
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
				require.NoError(t, os.Chmod(filepath.Dir(path), 0755))
			}
			registry, err := loadProfiles(t.Context(), path)
			require.NoError(t, err)
			require.Equal(t, emptyProfileRegistry(), registry)
			_, err = updateProfiles(t.Context(), path, func(*profileRegistry) error { return nil })
			require.ErrorContains(t, err, "private configuration directory")
			_, err = os.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}

func TestProfileStoreRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"registry-directory", "registry-symlink", "config-symlink", "lock-directory", "lock-symlink", "dangling-lock-symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := profileRegistryTestPath(t)
			target := filepath.Join(t.TempDir(), "target")
			writeProfileRegistryTestFile(t, target, emptyProfileJSON)
			if kind == "config-symlink" {
				writeProfileRegistryTestFile(t, filepath.Join(filepath.Dir(target), "profiles.json"), emptyProfileJSON)
				if err := os.Symlink(filepath.Dir(target), filepath.Dir(path)); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			} else {
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
				unsafePath := path
				if strings.Contains(kind, "lock") {
					unsafePath = filepath.Join(filepath.Dir(path), ".profiles.json.lock")
				}
				if strings.HasSuffix(kind, "directory") {
					require.NoError(t, os.Mkdir(unsafePath, 0700))
				} else {
					linkTarget := target
					if kind == "dangling-lock-symlink" {
						linkTarget += "-missing"
					}
					if err := os.Symlink(linkTarget, unsafePath); err != nil {
						t.Skipf("symlinks unavailable: %v", err)
					}
				}
			}
			if !strings.Contains(kind, "lock") {
				_, err := loadProfiles(t.Context(), path)
				require.Error(t, err)
			}
			_, err := updateProfiles(t.Context(), path, func(*profileRegistry) error { t.Fatal("editor called"); return nil })
			require.Error(t, err)
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			require.Equal(t, emptyProfileJSON, string(data))
		})
	}
}

func TestProfileStoreRejectsInsecurePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows inherits the configuration directory ACL; Unix permission bits do not verify it")
	}
	for _, target := range []string{"registry", "directory", "lock"} {
		t.Run(target, func(t *testing.T) {
			path := profileRegistryTestPath(t)
			writeProfileRegistryTestFile(t, path, emptyProfileJSON)
			switch target {
			case "registry":
				require.NoError(t, os.Chmod(path, 0644))
			case "directory":
				require.NoError(t, os.Chmod(filepath.Dir(path), 0755))
			case "lock":
				lockPath := filepath.Join(filepath.Dir(path), ".profiles.json.lock")
				require.NoError(t, os.WriteFile(lockPath, nil, 0600))
				require.NoError(t, os.Chmod(lockPath, 0666))
			}
			if target != "lock" {
				_, err := loadProfiles(t.Context(), path)
				require.ErrorContains(t, err, "private")
			}
			_, err := updateProfiles(t.Context(), path, func(*profileRegistry) error { t.Fatal("editor called"); return nil })
			require.ErrorContains(t, err, "private")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, emptyProfileJSON, string(data))
		})
	}
}
