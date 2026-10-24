//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

type ownedWindowsXMLFile struct{ *os.File }

func openOwnedWindowsXML(path string) (ownedXMLFile, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, errors.New("xml_path")
	}
	handle, err := windows.CreateFile(name, windows.GENERIC_READ|windows.DELETE, 0, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	kind, kindErr := windows.GetFileType(handle)
	if windows.GetFileInformationByHandle(handle, &info) != nil || kindErr != nil || kind != windows.FILE_TYPE_DISK || info.FileAttributes&(windows.FILE_ATTRIBUTE_REPARSE_POINT|windows.FILE_ATTRIBUTE_DIRECTORY) != 0 || info.NumberOfLinks != 1 {
		windows.CloseHandle(handle)
		return nil, errors.New("xml_handle_identity")
	}
	return &ownedWindowsXMLFile{os.NewFile(uintptr(handle), path)}, nil
}

func (f *ownedWindowsXMLFile) MarkDelete() error {
	disposition := byte(1)
	return windows.SetFileInformationByHandle(windows.Handle(f.Fd()), windows.FileDispositionInfo, &disposition, 1)
}

func TestOwnedWaitXMLPreparationProcess(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	args := os.Args[separator+1:]
	if len(args) != 1 {
		t.Fatal("owned-wait invalid_xml_preparation_arguments")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	result := prepareOwnedXML(ctx, args[0], openOwnedWindowsXML, func(result ownedXMLPreparation) error { return ownedXMLPublish(args[0], result) })
	if result.State != "released" {
		t.Fatalf("owned-wait xml_preparation_failed code=%s", result.Failure)
	}
}

func TestMainNativeShellOwnedWaitXMLReleaseControls(t *testing.T) {
	t.Run("same_handle_release", func(t *testing.T) {
		dir := ownedXMLFixture(t, ownedXMLFixtureDocument)
		var locked ownedXMLFile
		result := prepareOwnedXML(t.Context(), dir, func(path string) (ownedXMLFile, error) {
			file, err := openOwnedWindowsXML(path)
			if err != nil {
				return nil, err
			}
			locked = file
			// A replacement cannot enter between verification and disposition.
			if err := os.Rename(path, path+".replacement"); err == nil {
				t.Error("locked XML permitted replacement")
			}
			return file, nil
		}, func(result ownedXMLPreparation) error {
			if result.State == "prepared" {
				if locked == nil {
					t.Error("preparation did not retain the handle")
				}
				if file, err := os.OpenFile(filepath.Join(dir, "private-wait.xml"), os.O_WRONLY, 0); err == nil {
					file.Close()
					t.Error("locked XML permitted writing")
				}
			}
			return ownedXMLPublish(dir, result)
		})
		if result.State != "released" || !result.Complete || len(result.Anchors) != 1 {
			t.Fatalf("release failed: code=%s", result.Failure)
		}
		if _, err := os.Stat(filepath.Join(dir, "private-wait.xml")); !os.IsNotExist(err) {
			t.Fatal("released XML remains")
		}
	})
	t.Run("hard_link_preserved", func(t *testing.T) {
		dir := ownedXMLFixture(t, ownedXMLFixtureDocument)
		path := filepath.Join(dir, "private-wait.xml")
		if err := os.Link(path, path+".link"); err != nil {
			t.Fatalf("hard-link control unavailable: error_type=%T", err)
		}
		result := prepareOwnedXML(t.Context(), dir, openOwnedWindowsXML, func(result ownedXMLPreparation) error { return ownedXMLPublish(dir, result) })
		if result.Failure != "xml_open_identity" {
			t.Fatal("hard link was not rejected")
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal("hard-linked XML was removed")
		}
	})
	t.Run("reparse_point_preserved", func(t *testing.T) {
		dir := ownedXMLFixture(t, ownedXMLFixtureDocument)
		path := filepath.Join(dir, "private-wait.xml")
		target := filepath.Join(t.TempDir(), "owned-target.xml")
		if err := os.Rename(path, target); err != nil {
			t.Fatalf("prepare reparse control: error_type=%T", err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("native symlink unavailable: error_type=%T", err)
		}
		result := prepareOwnedXML(t.Context(), dir, openOwnedWindowsXML, func(result ownedXMLPreparation) error { return ownedXMLPublish(dir, result) })
		if result.Failure != "xml_open_identity" {
			t.Fatal("reparse point was not rejected")
		}
		if _, err := os.Lstat(path); err != nil {
			t.Fatal("reparse point was removed")
		}
		if _, err := os.Stat(target); err != nil {
			t.Fatal("owned reparse target was removed")
		}
	})

	t.Run("attribute_handle_delays_release", func(t *testing.T) {
		dir := ownedXMLFixture(t, ownedXMLFixtureDocument)
		path := filepath.Join(dir, "private-wait.xml")
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal("invalid owned path")
		}
		handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatalf("attribute handle control failed: error_type=%T", err)
		}
		closed := false
		defer func() {
			if !closed {
				windows.CloseHandle(handle)
			}
		}()
		result := prepareOwnedXML(t.Context(), dir, openOwnedWindowsXML, func(result ownedXMLPreparation) error { return ownedXMLPublish(dir, result) })
		if result.State != "failed" || result.Failure != "xml_release_unconfirmed" || result.Released != 0 {
			t.Fatalf("pending release was accepted: code=%s", result.Failure)
		}
		if err := windows.CloseHandle(handle); err != nil {
			t.Fatalf("attribute handle close failed: error_type=%T", err)
		}
		closed = true
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("final handle did not release XML")
		}
	})

}
