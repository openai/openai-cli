// Render the package-owned fish startup file without initializing the CLI.
package main

import (
	"fmt"
	"os"

	"github.com/openai/openai-cli/internal/autocomplete"
)

func main() {
	content, err := autocomplete.FishPackagePickerScript()
	if err == nil {
		_, err = os.Stdout.Write(content)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
