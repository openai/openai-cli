//go:build windows

package clihelp

import (
	"regexp"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// MSYS/Cygwin PTYs use these named pipes, including their native/Cygwin variants.
// Windows can place them inside a session's AppContainer namespace.
// Other named pipes and anonymous pipes remain redirected output.
var welcomePTYName = regexp.MustCompile(`^\\(?:Device\\NamedPipe\\)?` +
	`(?:Sessions\\[0-9]+\\AppContainerNamedObjects\\S-1-15-2(?:-[0-9]+){7}\\)?` +
	`(?:msys|cygwin)-[0-9a-fA-F]+-pty[0-9]+-(?:from|to)-master(?:-nat|-cyg)?$`)

func welcomePseudoTerminal(fd uintptr) bool {
	handle := windows.Handle(fd)
	kind, err := windows.GetFileType(handle)
	if err != nil || kind != windows.FILE_TYPE_PIPE {
		return false
	}
	var info struct {
		length uint32
		name   [windows.MAX_PATH]uint16
	}
	// Query handle metadata only. Do not read terminal input or send escape queries.
	if windows.GetFileInformationByHandleEx(handle, windows.FileNameInfo,
		(*byte)(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))) != nil {
		return false
	}
	if info.length == 0 || info.length%2 != 0 || info.length > uint32(len(info.name)*2) {
		return false
	}
	return welcomePTYName.MatchString(string(utf16.Decode(info.name[:info.length/2])))
}
