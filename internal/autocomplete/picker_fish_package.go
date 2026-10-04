package autocomplete

import "strings"

// FishPackagePickerScript renders the Linux package's vendor_conf.d script.
// It resolves the user's home and selected executable when the shell starts.
func FishPackagePickerScript() ([]byte, error) {
	return fishPackagePickerScript("/usr/bin/openai")
}

func fishPackagePickerScript(executable string) ([]byte, error) {
	guard, err := autoCompleteFS.ReadFile("shellscripts/fish_package_guard.fish")
	if err != nil {
		return nil, err
	}
	check := strings.ReplaceAll(string(guard), "__OPENAI_PACKAGE_EXECUTABLE__", quotePickerPath(CompletionStyleFish, executable))
	return renderPickerStartup(PickerInstallation{Shell: CompletionStyleFish}, check)
}
