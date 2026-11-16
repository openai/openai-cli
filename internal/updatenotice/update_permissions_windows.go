//go:build windows

package updatenotice

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A process may select another account's LOCALAPPDATA, including through a
// network mapping. Only a private, local, non-reparse installation is eligible.
func windowsInstallPath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || !localWindowsUpdatePath(base) {
		return "", errCache
	}
	return filepath.Join(base, "OpenAI", "CLI", "bin", "openai.exe"), nil
}

func localWindowsUpdatePath(path string) bool {
	if !filepath.IsAbs(path) || len(filepath.VolumeName(path)) != 2 || strings.Contains(path[2:], ":") {
		return false
	}
	drive, err := windows.UTF16PtrFromString(filepath.VolumeName(path) + `\`)
	return err == nil && windows.GetDriveType(drive) == windows.DRIVE_FIXED
}

func openWindowsManagedBin() (*os.Root, error) {
	path, err := windowsInstallPath()
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(path)
	if err := validateWindowsUpdatePath(dir, true); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	// Check the opened directory as well as its ancestry. The installed name is
	// used for CreateProcess, so a redirected absolute path must never qualify.
	leaf, err := root.Open(".")
	if err == nil {
		err = checkWindowsUpdateHandle(leaf, true, true)
		leaf.Close()
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	return root, nil
}

// validateWindowsUpdatePath checks the leaf's DACL and rejects paths where
// another principal can redirect or replace an existing ancestor.
func validateWindowsUpdatePath(path string, directory bool) error {
	return validateWindowsPath(path, directory, true)
}

func validateWindowsPath(path string, directory, privateLeaf bool) error {
	if !filepath.IsAbs(path) || len(filepath.VolumeName(path)) != 2 || filepath.Clean(path) != path {
		return errCache
	}
	if err := inspectWindowsUpdatePath(path, directory, privateLeaf); err != nil {
		return err
	}
	for p := filepath.Dir(path); ; p = filepath.Dir(p) {
		if err := inspectWindowsUpdatePath(p, true, false); err != nil {
			return err
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}

func inspectWindowsUpdatePath(path string, directory, private bool) error {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := windows.CreateFile(ptr, windows.FILE_READ_ATTRIBUTES|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return &os.PathError{Op: "inspect", Path: path, Err: err}
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	return checkWindowsUpdateHandle(file, directory, private)
}

func safeOpenedUpdateFile(file *os.File) bool {
	return checkWindowsUpdateHandle(file, false, true) == nil
}

func checkWindowsUpdateHandle(file *os.File, directory, private bool) error {
	handle := windows.Handle(file.Fd())
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if (info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0) != directory ||
		info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || (!directory && info.NumberOfLinks != 1) {
		return errCache
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil || !trustedUpdateSID(owner, user.User.Sid, private) {
		return errCache
	}
	acl, _, err := sd.DACL()
	if err != nil || acl == nil {
		return errCache
	}
	// An ordinary drive root can grant users permission to create a sibling.
	// Existing child names are protected only if strangers cannot delete a
	// child, redirect an ancestor, or change its owner/security descriptor.
	const modifyAncestor = windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.WRITE_DAC | windows.WRITE_OWNER |
		windows.DELETE | 0x0040 | windows.FILE_WRITE_ATTRIBUTES | windows.FILE_WRITE_EA
	unsafeAccess := windows.ACCESS_MASK(modifyAncestor)
	if !directory {
		// Other users can read an extracted ZIP executable. They must never
		// be able to modify the executable or its alternate data streams.
		unsafeAccess |= windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA
	}
	for n := uint32(0); n < uint32(acl.AceCount); n++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(acl, n, &ace); err != nil || ace == nil {
			return errCache
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE || (!private && ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0) {
			continue
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || int(ace.Header.AceSize) < int(unsafe.Sizeof(*ace)) {
			return errCache
		}
		if (!private && ace.Mask&unsafeAccess == 0) || ace.Mask == 0 {
			continue
		}
		if !trustedUpdateSID((*windows.SID)(unsafe.Pointer(&ace.SidStart)), user.User.Sid, private) {
			return errCache
		}
	}
	return nil
}

func trustedUpdateSID(sid, user *windows.SID, private bool) bool {
	if sid == nil || !sid.IsValid() {
		return false
	}
	if sid.Equals(user) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return true
	}
	const trustedInstaller = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	return !private && sid.String() == trustedInstaller
}

func ownedUpdatePath(info os.FileInfo) bool {
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	return ok && data.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT == 0
}

func sameWindowsName(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
