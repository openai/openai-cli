//go:build !windows

package scripts_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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

	for _, setupExit := range []int{0, 17} {
		t.Run(fmt.Sprintf("setup_exit_%d", setupExit), func(t *testing.T) {
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
			for _, version := range []string{"1.0", "1.1"} {
				staged := filepath.Join(root, "Caskroom user's $(literal) files", version)
				if err := os.MkdirAll(staged, 0o700); err != nil {
					t.Fatal(err)
				}
				fixture := "#!" + ruby + "\n" + homebrewPickerFixture
				if err := os.WriteFile(filepath.Join(staged, "openai"), []byte(fixture), 0o700); err != nil {
					t.Fatal(err)
				}
				command := exec.Command(ruby, "-e", homebrewPickerHookHarness, hookPath, staged)
				command.Env = []string{
					"PATH=" + os.Getenv("PATH"),
					"HOME=" + home,
					"SHELL=/bin/zsh",
					"PICKER_TEST_LOG=" + logPath,
					fmt.Sprintf("PICKER_TEST_EXIT=%d", setupExit),
				}
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("hook must not abort installation when setup exits %d: %v; %s", setupExit, err, output)
				}
				if !strings.Contains(string(output), "setup output") || !strings.Contains(string(output), "setup diagnostic") {
					t.Fatalf("hook hides setup output or diagnostics: %s", output)
				}
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			calls := strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(calls) != 2 {
				t.Fatalf("setup calls = %d, want one for each installation", len(calls))
			}
			for index, line := range calls {
				var call struct {
					Args       []string `json:"args"`
					Executable string   `json:"executable"`
					Home       string   `json:"home"`
					Shell      string   `json:"shell"`
					UID        int      `json:"uid"`
					Stdin      string   `json:"stdin"`
				}
				if err := json.Unmarshal([]byte(line), &call); err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(call.Args, []string{"@completion", "--install-picker", "--automatic"}) {
					t.Errorf("setup arguments = %q", call.Args)
				}
				wantExecutable := filepath.Join(root, "Caskroom user's $(literal) files", fmt.Sprintf("1.%d", index), "openai")
				if call.Executable != wantExecutable || call.Home != home || call.Shell != "/bin/zsh" || call.UID != os.Geteuid() || call.Stdin != "" {
					t.Errorf("hook changed setup executable, user, environment or stdin: %+v", call)
				}
			}
			contents, err := os.ReadFile(profile)
			if err != nil || string(contents) != string(profileContents) {
				t.Fatalf("packaging hook edited the profile outside the shared setup routine: %q, %v", contents, err)
			}
		})
	}
}

// Evaluate the real cask hook using the documented system_command call shape.
// No Homebrew installation or user profile writes occur in this fixture.
const homebrewPickerHookHarness = `
require "open3"
def staged_path
  ARGV.fetch(1)
end
def system_command(executable, args: [], sudo: false, must_succeed: true,
                   print_stdout: false, print_stderr: true)
  raise "picker setup must not use sudo" if sudo
  out, err, status = Open3.capture3(executable, *args, stdin_data: "")
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
                          shell: ENV["SHELL"], uid: Process.euid, stdin: STDIN.read)
end
puts "setup output"
warn "setup diagnostic"
exit Integer(ENV.fetch("PICKER_TEST_EXIT"))
`
