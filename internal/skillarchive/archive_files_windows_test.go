//go:build windows

package skillarchive

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestArchiveTemporaryPermissionsWindows(t *testing.T) {
	archive, _ := prepareCleanupFixture(t)
	directory, err := archive.staging.root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer directory.Close()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []*os.File{directory, archive.file} {
		descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		control, _, err := descriptor.Control()
		if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
			t.Fatalf("temporary DACL is not protected: %v", err)
		}
		dacl, _, err := descriptor.DACL()
		if err != nil || dacl == nil || dacl.AceCount != 3 {
			t.Fatalf("temporary DACL differs from three private grants: %v", err)
		}
		for i := uint32(0); i < uint32(dacl.AceCount); i++ {
			var ace *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, i, &ace); err != nil {
				t.Fatal(err)
			}
			if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
				t.Fatal("unexpected temporary access rule")
			}
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sid.IsValid() || !(sid.Equals(user.User.Sid) || sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid)) {
				t.Fatal("temporary access rule grants another account access")
			}
		}
	}
}
