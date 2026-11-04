//go:build darwin || linux

package updatenotice

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func verifierFixture(t *testing.T, platform, member string, body []byte) []byte {
	t.Helper()
	var data bytes.Buffer
	if strings.HasPrefix(platform, "windows_") {
		w := zip.NewWriter(&data)
		f, err := w.Create(member)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&data)
		w := tar.NewWriter(gz)
		if err := w.WriteHeader(&tar.Header{Name: member, Mode: 0755, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	return data.Bytes()
}

func TestInstallPrivateVerifier(t *testing.T) {
	for _, platform := range []string{"linux_arm64", "windows_amd64"} {
		t.Run(platform, func(t *testing.T) {
			basename := "gh_" + verifierVersion + "_" + platform
			archiveName, member, program := basename+".tar.gz", basename+"/bin/gh", "gh-"+verifierVersion
			if strings.HasPrefix(platform, "windows_") {
				archiveName, member, program = basename+".zip", "bin/gh.exe", program+".exe"
			}
			binary := []byte("synthetic verifier bytes")
			for _, scenario := range []string{"install", "retry", "after_link", "wrong_archive", "wrong_binary", "wrong_member", "occupied", "symlink", "concurrent"} {
				t.Run(scenario, func(t *testing.T) {
					entry := member
					if scenario == "wrong_member" {
						entry = "unrelated/gh"
					}
					archive := verifierFixture(t, platform, entry, binary)
					pin := verifierPin{archive: fmt.Sprintf("%x", sha256.Sum256(archive)), binary: fmt.Sprintf("%x", sha256.Sum256(binary))}
					switch scenario {
					case "wrong_archive":
						pin.archive = strings.Repeat("0", 64)
					case "wrong_binary":
						pin.binary = strings.Repeat("0", 64)
					}
					dir := t.TempDir()
					if err := os.Chmod(dir, 0700); err != nil {
						t.Fatal(err)
					}
					root, err := os.OpenRoot(dir)
					if err != nil {
						t.Fatal(err)
					}
					defer root.Close()
					final := filepath.Join(dir, program)
					calls := 0
					d := releaseDelivery{client: &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
						calls++
						if r.URL.String() != "https://github.com/cli/cli/releases/download/v"+verifierVersion+"/"+archiveName || r.Header.Get("Authorization") != "" {
							t.Fatalf("Unexpected verifier request to host %s", r.URL.Host)
						}
						return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(archive))}, nil
					})}}
					switch scenario {
					case "retry", "after_link":
						if err := os.WriteFile(final, binary, 0700); err != nil {
							t.Fatal(err)
						}
						if scenario == "after_link" {
							if err := os.Link(final, filepath.Join(dir, "gh.download")); err != nil {
								t.Fatal(err)
							}
						}
					case "occupied":
						if err := os.WriteFile(final, []byte("existing"), 0700); err != nil {
							t.Fatal(err)
						}
					case "symlink":
						if err := os.Symlink("outside", final); err != nil {
							t.Fatal(err)
						}
					case "concurrent":
						cache, err := openCache(t.Context(), dir)
						if err != nil {
							t.Fatal(err)
						}
						defer cache.close()
					}
					path, err := d.installPrivateVerifier(t.Context(), root, platform, program, pin, false)
					switch scenario {
					case "install", "retry", "after_link":
						if err != nil || path != final {
							t.Fatalf("Expected private verifier at %s, got %s, %v", final, path, err)
						}
						data, err := os.ReadFile(final)
						if err != nil || !bytes.Equal(data, binary) {
							t.Fatalf("Wrong verifier contents, %v", err)
						}
					case "occupied":
						data, readErr := os.ReadFile(final)
						if err == nil || readErr != nil || string(data) != "existing" {
							t.Fatal("Overwrote or accepted an occupied verifier path")
						}
					case "symlink":
						if target, readErr := os.Readlink(final); err == nil || readErr != nil || target != "outside" {
							t.Fatal("Followed or replaced a symbolic link")
						}
					default:
						if _, statErr := os.Lstat(final); err == nil || !os.IsNotExist(statErr) {
							t.Fatal("Published a verifier after a failed check")
						}
					}
					wantCalls := 0
					switch scenario {
					case "install", "wrong_archive", "wrong_binary", "wrong_member":
						wantCalls = 1
					}
					if calls != wantCalls {
						t.Fatalf("Downloaded %d times, want %d", calls, wantCalls)
					}
					if _, err := os.Lstat(filepath.Join(dir, "gh.download")); !os.IsNotExist(err) {
						t.Fatal("Left staging file behind")
					}
				})
			}
		})
	}
}
