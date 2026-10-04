//go:build !windows

package scripts_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHomebrewPickerInstallHook(t *testing.T) {
	config := readReleaseYAML[goReleaserConfig](t, filepath.Join("..", ".goreleaser.yml"))
	if len(config.HomebrewCasks) != 1 {
		t.Fatal("expected one Homebrew cask")
	}
	hooks := config.HomebrewCasks[0].Hooks
	if len(hooks.Pre) != 0 || len(hooks.Post) != 1 || hooks.Post["install"] == "" {
		t.Fatal("picker setup must only run after installation; uninstall hooks also run during upgrades")
	}
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("Ruby is required to execute the Homebrew hook fixture")
	}

	for _, environment := range []string{"filtered", "saved_precedence", "unfiltered", "unset"} {
		for _, setupExit := range []int{0, 17} {
			t.Run(fmt.Sprintf("%s/setup_exit_%d", environment, setupExit), func(t *testing.T) {
				root := t.TempDir()
				home := filepath.Join(root, "user home")
				if err := os.Mkdir(home, 0o700); err != nil {
					t.Fatal(err)
				}
				profile := filepath.Join(home, ".zshrc")
				profileContents := []byte("# existing profile remains owned by the CLI setup routine\n")
				if err := os.WriteFile(profile, profileContents, 0o600); err != nil {
					t.Fatal(err)
				}
				hookPath := filepath.Join(root, "hook.rb")
				if err := os.WriteFile(hookPath, []byte(hooks.Post["install"]), 0o600); err != nil {
					t.Fatal(err)
				}
				logPath := filepath.Join(root, "calls.jsonl")
				config := filepath.Join(home, "custom config $(literal)")
				zdotdir := filepath.Join(home, "zsh's startup")
				optOut := filepath.Join(home, ".openai", "shell", "image-picker.tab-off-zsh")
				if err := os.MkdirAll(filepath.Dir(optOut), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(optOut, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				otherBin := filepath.Join(root, "other bin")
				if err := os.Mkdir(otherBin, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(otherBin, "openai"), []byte("#!/bin/sh\nexit 91\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				versions := []string{"1.0", "1.1", "1.1", "1.0"} // Install, upgrade, repeat, rollback.
				for _, version := range versions {
					staged := filepath.Join(root, "Caskroom user's $(literal) files", version)
					if err := os.MkdirAll(staged, 0o700); err != nil {
						t.Fatal(err)
					}
					fixture := "#!" + ruby + "\n" + homebrewPickerFixture
					if err := os.WriteFile(filepath.Join(staged, "openai"), []byte(fixture), 0o700); err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
					command := exec.CommandContext(ctx, ruby, "-e", homebrewPickerHookHarness, hookPath, staged)
					command.Env = []string{
						"PATH=" + otherBin + ":/usr/bin:/bin",
						"HOME=" + home,
						"SHELL=/bin/zsh",
						"PICKER_TEST_LOG=" + logPath,
						fmt.Sprintf("PICKER_TEST_EXIT=%d", setupExit),
					}
					switch environment {
					case "filtered", "saved_precedence":
						command.Env = append(command.Env, "HOMEBREW_ZDOTDIR="+zdotdir, "HOMEBREW_XDG_CONFIG_HOME="+config,
							"HOMEBREW_SUDO_USER=fixture-user")
						if environment == "saved_precedence" {
							command.Env = append(command.Env, "ZDOTDIR=wrong", "XDG_CONFIG_HOME=wrong", "SUDO_USER=wrong")
						}
					case "unfiltered":
						command.Env = append(command.Env, "ZDOTDIR="+zdotdir, "XDG_CONFIG_HOME="+config, "SUDO_USER=fixture-user")
					}
					var stdout, stderr bytes.Buffer
					command.Stdout, command.Stderr = &stdout, &stderr
					err := command.Run()
					cancel()
					if err != nil {
						t.Fatalf("hook must not abort installation when setup exits %d: %v; %s", setupExit, err, stderr.String())
					}
					if stdout.String() != "setup output\n" || stderr.String() != "setup diagnostic\n" {
						t.Fatalf("hook output: stdout=%q stderr=%q", stdout.String(), stderr.String())
					}
				}
				data, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				calls := strings.Split(strings.TrimSpace(string(data)), "\n")
				if len(calls) != len(versions) {
					t.Fatalf("setup calls = %d, want one for each installation", len(calls))
				}
				for index, line := range calls {
					var call struct {
						Args       []string `json:"args"`
						Executable string   `json:"executable"`
						Home       string   `json:"home"`
						Shell      string   `json:"shell"`
						UID        int      `json:"uid"`
						Zdotdir    string   `json:"zdotdir"`
						Config     string   `json:"config"`
						SudoUser   string   `json:"sudo_user"`
						Stdin      string   `json:"stdin"`
					}
					if err := json.Unmarshal([]byte(line), &call); err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(call.Args, []string{"@completion", "--install-picker", "--automatic"}) {
						t.Errorf("setup arguments = %q", call.Args)
					}
					wantExecutable := filepath.Join(root, "Caskroom user's $(literal) files", versions[index], "openai")
					if environment == "unset" {
						if call.Zdotdir != "" || call.Config != "" || call.SudoUser != "" {
							t.Errorf("hook invented unset configuration: %+v", call)
						}
					} else if call.Zdotdir != zdotdir || call.Config != config || call.SudoUser != "fixture-user" {
						t.Errorf("hook lost shell configuration or sudo marker: %+v", call)
					}
					if call.Executable != wantExecutable || call.Home != home || call.Shell != "/bin/zsh" || call.UID != os.Geteuid() || call.Stdin != "" {
						t.Errorf("hook changed setup executable, user, environment or stdin: %+v", call)
					}
				}
				marker, err := os.ReadFile(optOut)
				if err != nil || len(marker) != 0 {
					t.Fatalf("hook changed existing opt-out: %q, %v", marker, err)
				}
				contents, err := os.ReadFile(profile)
				if err != nil || string(contents) != string(profileContents) {
					t.Fatalf("packaging hook edited the profile outside the shared setup routine: %q, %v", contents, err)
				}
			})
		}
	}
}

// Homebrew raises on timeout even with must_succeed: false. Only that optional
// failure is swallowed; cancellation and unexpected hook errors still abort.
func TestHomebrewPickerHookExceptions(t *testing.T) {
	config := readReleaseYAML[goReleaserConfig](t, filepath.Join("..", ".goreleaser.yml"))
	if len(config.HomebrewCasks) != 1 {
		t.Fatal("expected one Homebrew cask")
	}
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("Ruby is required to execute the Homebrew hook fixture")
	}
	for _, failure := range []string{"Timeout::Error", "Interrupt", "RuntimeError"} {
		t.Run(failure, func(t *testing.T) {
			home := t.TempDir()
			hook := filepath.Join(home, "hook.rb")
			if err := os.WriteFile(hook, []byte(config.HomebrewCasks[0].Hooks.Post["install"]), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(t.Context(), ruby, "-e", `
require "timeout"
def staged_path; "/synthetic/cask"; end
def system_command(*args, **options)
  raise "optional setup must have a bounded wait" unless options[:timeout] && options[:timeout] > 0 && options[:timeout] <= 10
  raise Object.const_get(ARGV.fetch(1))
end
eval(File.read(ARGV.fetch(0)), binding, ARGV.fetch(0))
puts "installation continued"
`, hook, failure)
			command.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			err := command.Run()
			if failure == "Timeout::Error" {
				if err != nil || stdout.String() != "installation continued\n" || stderr.String() != "Tab shortcut setup timed out; installation will continue.\n" {
					t.Fatalf("timeout did not remain optional: %v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
				}
			} else if err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), failure) {
				t.Fatalf("hook swallowed %s: %v stdout=%q stderr=%q", failure, err, stdout.String(), stderr.String())
			}
		})
	}
}

// Evaluate the real cask hook using the documented system_command call shape.
// This is a command-contract fixture, not a real Homebrew install or CLI setup.
// Profiles and consent are only inspected here; setup behavior has separate tests.
const homebrewPickerHookHarness = `
require "open3"
def staged_path
  ARGV.fetch(1)
end
def system_command(executable, args: [], env: {}, sudo: false, must_succeed: true,
                   print_stdout: false, print_stderr: true, timeout: nil)
  raise "picker setup must not use sudo" if sudo
  raise "optional setup must have a bounded wait" unless timeout && timeout > 0 && timeout <= 10
  out, err, status = Open3.capture3(env, executable, *args, stdin_data: "")
  $stdout.write(out) if print_stdout
  $stderr.write(err) if print_stderr
  raise "setup failed" if must_succeed && !status.success?
end
eval(File.read(ARGV.fetch(0)), binding, ARGV.fetch(0))
`

const homebrewPickerFixture = `
require "json"
File.open(ENV.fetch("PICKER_TEST_LOG"), "a") do |file|
  file.puts JSON.generate(args: ARGV, executable: $0, home: ENV["HOME"],
                          shell: ENV["SHELL"], uid: Process.euid, stdin: STDIN.read,
                          zdotdir: ENV["ZDOTDIR"], config: ENV["XDG_CONFIG_HOME"], sudo_user: ENV["SUDO_USER"])
end
puts "setup output"
warn "setup diagnostic"
exit Integer(ENV.fetch("PICKER_TEST_EXIT"))
`
