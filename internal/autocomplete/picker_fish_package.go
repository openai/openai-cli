package autocomplete

import "strings"

// FishPackagePickerScript renders the Linux package's vendor_conf.d script.
// It checks consent at activation and the selected executable at every Tab press.
func FishPackagePickerScript() ([]byte, error) {
	return fishPackagePickerScript("/usr/bin/openai")
}

func fishPackagePickerScript(executable string) ([]byte, error) {
	guard, err := autoCompleteFS.ReadFile("shellscripts/fish_package_guard.fish")
	if err != nil {
		return nil, err
	}
	binary := quotePickerPath(CompletionStyleFish, executable)
	check := strings.ReplaceAll(string(guard), "__OPENAI_PACKAGE_EXECUTABLE__", binary)
	// The system test supports -ef on older fish versions too. Its absolute
	// path keeps automatic activation independent of other executables on PATH.
	picker, err := renderPickerCompletionWithGuard(CompletionStyleFish, "openai", "test -x /usr/bin/test; and command /usr/bin/test (command -s openai) -ef "+binary+" 2>/dev/null")
	if err != nil {
		return nil, err
	}
	return renderPickerStartup(PickerInstallation{Shell: CompletionStyleFish}, "", picker, check), nil
}
