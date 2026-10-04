//go:build !windows

package scripts_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLinuxFishPickerPackageArtifacts(t *testing.T) {
	goreleaser := verifiedReleaseExecutable(t)
	config, err := os.ReadFile("../.goreleaser.yml")
	if err != nil {
		t.Fatal(err)
	}
	_, packaging, ok := strings.Cut(string(config), "nfpms:\n")
	if !ok {
		t.Fatal("missing package configuration")
	}
	packaging, _, ok = strings.Cut(packaging, "homebrew_casks:\n")
	if !ok {
		t.Fatal("missing package section boundary")
	}
	root := t.TempDir()
	const picker = "# synthetic fish package integration\n"
	files := map[string]string{
		"go.mod":                         "module example.com/package-fixture\n\ngo 1.25.0\n",
		"main.go":                        "package main\nfunc main() {}\n",
		".gitignore":                     "dist/\n",
		".goreleaser.yml":                "version: 2\nproject_name: openai\nrelease:\n  disable: true\nbuilds:\n  - main: .\n    goos: [linux]\n    goarch: [amd64]\n    env: [CGO_ENABLED=0]\nnfpms:\n" + packaging,
		"man/man1/openai.1.gz":           "synthetic manual",
		"completions/picker/openai.fish": picker,
	}
	for relative, data := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	runHistoricalFixtureCommand(t, root, "git", "init", "--quiet")
	runHistoricalFixtureCommand(t, root, "git", "remote", "add", "origin", "https://github.com/example/package-fixture.git")
	runHistoricalFixtureCommand(t, root, "git", "add", ".")
	runHistoricalFixtureCommand(t, root, "git", "-c", "user.name=Package Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "synthetic package fixture")
	command := exec.Command(goreleaser, "release", "--snapshot", "--clean", "--skip=publish")
	command.Dir = root
	for _, variable := range os.Environ() {
		name, _, _ := strings.Cut(variable, "=")
		if !slices.Contains(releaseCredentials, name) {
			command.Env = append(command.Env, variable)
		}
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("synthetic package build failed: %v\n%s", err, output)
	}
	for _, pattern := range []string{"*.apk", "*.deb", "*.rpm", "*.pkg.tar.zst"} {
		paths, err := filepath.Glob(filepath.Join(root, "dist", pattern))
		if err != nil || len(paths) != 1 {
			t.Fatalf("package %s = %v, error %v; expected one Linux amd64 artifact", pattern, paths, err)
		}
	}
	// APK uses separate gzip/tar members for package control scripts and files.
	// Inspect actual nFPM output, including ownership and absence of install hooks.
	apks, _ := filepath.Glob(filepath.Join(root, "dist", "*.apk"))
	archive, err := os.ReadFile(apks[0])
	if err != nil {
		t.Fatal(err)
	}
	archiveBuffer := bytes.NewBuffer(archive)
	installed := 0
	for archiveBuffer.Len() > 0 {
		compressed, err := gzip.NewReader(archiveBuffer)
		if err != nil {
			t.Fatal(err)
		}
		compressed.Multistream(false)
		reader := tar.NewReader(compressed)
		for {
			header, err := reader.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(header.Name, "install") || strings.Contains(header.Name, "upgrade") || strings.Contains(header.Name, "deinstall") {
				t.Errorf("unexpected package lifecycle hook: %s", header.Name)
			}
			if strings.HasPrefix(header.Name, "usr/share/openai/") {
				t.Errorf("unnecessary secondary picker file: %s", header.Name)
			}
			if header.Name != "usr/share/fish/vendor_conf.d/openai-picker.fish" {
				continue
			}
			installed++
			data, err := io.ReadAll(reader)
			if err != nil || string(data) != picker {
				t.Fatalf("packaged picker = %q, error = %v", data, err)
			}
			if header.Typeflag != tar.TypeReg || header.Mode != 0644 || header.Uid != 0 || header.Gid != 0 {
				t.Errorf("picker must be a root-owned regular file with mode 0644: %+v", header)
			}
		}
		if _, err := io.Copy(io.Discard, compressed); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if installed != 1 {
		t.Fatalf("installed picker files = %d, want exactly one", installed)
	}
}
