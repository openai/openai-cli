package clihelp

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func quoteInvocation(name, fallback string) string {
	if invocationParentIsCMD() {
		// cmd expands % and ! even inside quotes; a literal quote would end
		// the filename. Use the ordinary PATH invocation for these paths.
		if strings.ContainsAny(name, "%!\"") {
			return fallback
		}
		return `"` + name + `"`
	}
	// PowerShell is also the documented default for an unknown parent.
	return "& '" + strings.ReplaceAll(name, "'", "''") + "'"
}

func invocationParentIsCMD() bool {
	parent, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(os.Getppid()))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(parent)
	var path [32768]uint16
	size := uint32(len(path))
	if err := windows.QueryFullProcessImageName(parent, 0, &path[0], &size); err != nil {
		return false
	}
	return strings.EqualFold(filepath.Base(windows.UTF16ToString(path[:size])), "cmd.exe")
}
