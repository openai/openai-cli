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

func TestLinuxPickerPackageArtifacts(t *testing.T) {
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
	files := map[string][]byte{
		"go.mod":               []byte("module example.com/package-fixture\n\ngo 1.25.0\n"),
		"main.go":              []byte("package main\nfunc main() {}\n"),
		".gitignore":           []byte("dist/\n"),
		".goreleaser.yml":      []byte("version: 2\nproject_name: openai\nrelease:\n  disable: true\nbuilds:\n  - main: .\n    goos: [linux]\n    goarch: [amd64]\n    env: [CGO_ENABLED=0]\nnfpms:\n" + packaging),
		"man/man1/openai.1.gz": []byte("synthetic manual"),
	}
	for _, shell := range []string{"fish"} {
		files["completions/picker/openai."+shell] = []byte("# synthetic " + shell + " package completion\n")
	}
	for _, name := range []string{"linux-picker.fish"} {
		files["scripts/"+name], err = os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	for relative, data := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
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
	// APK is a concatenation of gzip tar members. Check both package control
	// scripts and data, using the actual nFPM output rather than only its YAML.
	apks, _ := filepath.Glob(filepath.Join(root, "dist", "*.apk"))
	archive, err := os.ReadFile(apks[0])
	if err != nil {
		t.Fatal(err)
	}
	// Each gzip member contains a separate tar archive.
	found := map[string][]byte{}
	archiveBuffer := bytes.NewBuffer(archive)
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
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			found[header.Name] = data
		}
		if _, err := io.Copy(io.Discard, compressed); err != nil {
			t.Fatal(err)
		}
		if err := compressed.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"usr/share/openai/shell/openai.fish", "usr/share/fish/vendor_conf.d/openai-picker.fish"} {
		if len(found[path]) == 0 {
			t.Errorf("APK omitted %s", path)
		}
	}
}

type linuxPickerPackage struct {
	Formats  []string          `yaml:"formats"`
	Scripts  map[string]string `yaml:"scripts"`
	Contents []struct {
		Source      string `yaml:"src"`
		Destination string `yaml:"dst"`
		FileInfo    struct {
			Mode int `yaml:"mode"`
		} `yaml:"file_info"`
	} `yaml:"contents"`
	Overrides map[string]struct {
		Scripts map[string]string `yaml:"scripts"`
	} `yaml:"overrides"`
	APK struct {
		Scripts map[string]string `yaml:"scripts"`
	} `yaml:"apk"`
	Arch struct {
		Scripts map[string]string `yaml:"scripts"`
	} `yaml:"archlinux"`
}

func TestLinuxPickerPackageContents(t *testing.T) {
	config := readReleaseYAML[struct {
		Packages []linuxPickerPackage `yaml:"nfpms"`
	}](t, "../.goreleaser.yml")
	if len(config.Packages) != 1 {
		t.Fatal("expected existing single Linux package configuration")
	}
	pkg := config.Packages[0]
	if len(pkg.Scripts) != 0 || len(pkg.Overrides) != 0 || len(pkg.APK.Scripts) != 0 || len(pkg.Arch.Scripts) != 0 {
		t.Fatal("package integration must not edit shell-owned startup files")
	}
	for _, destination := range []string{"/usr/share/openai/shell/", "/usr/share/fish/vendor_conf.d/openai-picker.fish"} {
		found := false
		for _, content := range pkg.Contents {
			if content.Destination == destination {
				found = true
				if content.FileInfo.Mode != 0644 {
					t.Errorf("%s is not world-readable: %o", destination, content.FileInfo.Mode)
				}
			}
		}
		if !found {
			t.Errorf("package omitted %s", destination)
		}
	}
}

func TestLinuxPickerReleaseInputVerification(t *testing.T) {
	workflow := readReleaseYAML[releaseWorkflow](t, "../.github/workflows/publish-release.yml")
	job := workflow.Jobs["goreleaser"]
	verify := job.Steps[releaseStepIndex(t, job, "Verify isolated release inputs")].Run
	for _, scenario := range []string{"current", "historical", "missing", "symlink", "unexpected", "historical-extra"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			runHistoricalFixtureCommand(t, root, "git", "init", "--quiet")
			config := "version: 2\n"
			if !strings.HasPrefix(scenario, "historical") {
				config += "# src: completions/picker/openai.fish\n"
			}
			if err := os.WriteFile(filepath.Join(root, ".goreleaser.yml"), []byte(config), 0644); err != nil {
				t.Fatal(err)
			}
			staging := t.TempDir()
			inputs := []string{"completions/openai.bash", "completions/openai.zsh", "completions/openai.fish", "man/man1/openai.1.gz"}
			if scenario != "historical" {
				inputs = append(inputs, "completions/picker/openai.fish")
			}
			if scenario == "unexpected" {
				inputs = append(inputs, "completions/picker/unreviewed.sh")
			}
			for _, relative := range inputs {
				path := filepath.Join(staging, relative)
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				if relative == "completions/picker/openai.fish" {
					if scenario == "missing" {
						continue
					}
					if scenario == "symlink" {
						if err := os.Symlink(filepath.Join(staging, "completions", "openai.bash"), path); err != nil {
							t.Fatal(err)
						}
						continue
					}
				}
				if err := os.WriteFile(path, []byte("synthetic shell completion\n"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command("bash", "-c", verify)
			command.Dir = root
			command.Env = append(os.Environ(), "RELEASE_INPUT_DIR="+staging)
			output, err := command.CombinedOutput()
			pass := scenario == "current" || scenario == "historical"
			if (err == nil) != pass {
				t.Fatalf("verification: %v; output=%s", err, output)
			}
			if pass {
				for _, relative := range inputs {
					if _, err := os.Stat(filepath.Join(root, relative)); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
