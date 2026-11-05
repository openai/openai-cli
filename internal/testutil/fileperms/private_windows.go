//go:build windows

// Package fileperms checks private file permissions in native-platform tests.
package fileperms

import (
	"golang.org/x/sys/windows"
	"os"
	"testing"
	"unsafe"
)

// AssertPrivate checks the explicit owner, SYSTEM, and administrator access grants.
func AssertPrivate(t *testing.T, file *os.File) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetSecurityInfo(windows.Handle(file.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("download DACL must be protected: %v", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("download requires an explicit DACL: %v", err)
	}
	var caller, system, administrators bool
	const fileAllAccess = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			t.Fatal(err)
		}
		if ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Header.AceFlags&windows.INHERITED_ACE != 0 || ace.Mask&fileAllAccess != fileAllAccess {
			t.Fatal("download access rule is not an explicit full-access grant")
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user.User.Sid):
			caller = true
		case sid.IsWellKnown(windows.WinLocalSystemSid):
			system = true
		case sid.IsWellKnown(windows.WinBuiltinAdministratorsSid):
			administrators = true
		default:
			t.Fatal("download grants access to an unexpected principal")
		}
	}
	// The normal runner has a distinct user SID. SYSTEM execution can combine these identities.
	system = system || user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) && caller
	administrators = administrators || user.User.Sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) && caller
	if !caller || !system || !administrators {
		t.Fatal("download is missing an intended access grant")
	}
}
