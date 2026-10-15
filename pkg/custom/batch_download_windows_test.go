//go:build windows

package custom

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestWindowsBatchDownloadPrivateStagingAndPublication(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "result.jsonl")
	payload := []byte("{\"custom_id\":\"synthetic\"}\r\n\x00\xff")
	reader := bytes.NewReader(payload)
	var staged os.FileInfo
	closed := false
	body := batchDownloadReader{
		read: func(p []byte) (int, error) {
			if staged == nil {
				entries, err := os.ReadDir(directory)
				require.NoError(t, err)
				require.Len(t, entries, 1)
				require.NotEqual(t, filepath.Base(path), entries[0].Name())
				stage, err := os.Open(filepath.Join(directory, entries[0].Name()))
				require.NoError(t, err)
				defer func() { require.NoError(t, stage.Close()) }()
				staged, err = stage.Stat()
				require.NoError(t, err)
				require.True(t, staged.Mode().IsRegular())
				require.Zero(t, staged.Size())
				assertPrivateWindowsDownload(t, stage)
			}
			return reader.Read(p)
		},
		close: func() error {
			closed = true
			return nil
		},
	}
	written, err := writeBatchDownload(t.Context(), path, body)
	require.NoError(t, err)
	require.NotNil(t, staged)
	require.True(t, closed)
	require.Equal(t, int64(len(payload)), written)
	file, err := os.Open(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	assertPrivateWindowsDownload(t, file)
	published, err := file.Stat()
	require.NoError(t, err)
	require.True(t, os.SameFile(staged, published))
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, payload, data)
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Equal(t, filepath.Base(path), entries[0].Name())
}

func TestWindowsBatchOutputDirectoryPermissions(t *testing.T) {
	setInheritedAccess := func(path, flags string) {
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		require.NoError(t, err)
		descriptor, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;" + flags + ";FR;;;WD)")
		require.NoError(t, err)
		dacl, _, err := descriptor.DACL()
		require.NoError(t, err)
		require.NoError(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
	}
	for _, inheritance := range []string{"OICI", "OICIIO", "CIIO"} {
		t.Run("new_"+inheritance, func(t *testing.T) {
			parent := t.TempDir()
			setInheritedAccess(parent, inheritance)
			path := filepath.Join(parent, "results")
			destination, err := prepareBatchOutputDirectory(t.Context(), path)
			require.NoError(t, err)
			file, err := os.Open(path)
			require.NoError(t, err)
			defer file.Close()
			assertPrivateWindowsDownload(t, file)
			info, err := file.Stat()
			require.NoError(t, err)
			require.True(t, os.SameFile(destination.info, info))
			payload := []byte("synthetic\r\n\x00\xff")
			result := filepath.Join(path, "output.jsonl")
			_, err = writeBatchDownloadInDirectory(t.Context(), path, "output.jsonl", io.NopCloser(bytes.NewReader(payload)), destination.info)
			require.NoError(t, err)
			saved, err := os.Open(result)
			require.NoError(t, err)
			defer saved.Close()
			assertPrivateWindowsDownload(t, saved)
			data, err := io.ReadAll(saved)
			require.NoError(t, err)
			require.Equal(t, payload, data)
		})
	}
	for _, existingResult := range []bool{false, true} {
		name := "existing_empty"
		if existingResult {
			name = "existing_result"
		}
		t.Run(name, func(t *testing.T) {
			path := t.TempDir()
			setInheritedAccess(path, "OICI")
			if existingResult {
				require.NoError(t, os.WriteFile(filepath.Join(path, "output.jsonl"), []byte("preserved"), 0600))
			}
			before, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			require.NoError(t, err)
			original, err := os.Stat(path)
			require.NoError(t, err)
			_, err = prepareBatchOutputDirectory(t.Context(), path)
			if existingResult {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			after, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
			require.NoError(t, err)
			current, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, before.String(), after.String())
			require.True(t, os.SameFile(original, current))
		})
	}
}

func TestWindowsBatchOutputDirectoryPaths(t *testing.T) {
	for _, name := range []string{"relative", "drive_relative", "long_absolute", "long_relative", "extended", "unicode", "trailing_separator", "junction"} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			t.Chdir(parent)
			prefix := parent
			if strings.HasPrefix(name, "long_") {
				for len(prefix) < 300 {
					prefix = filepath.Join(prefix, "long-directory-component")
				}
				require.NoError(t, os.MkdirAll(prefix, 0700))
			}
			if name == "junction" {
				target := filepath.Join(parent, "target")
				require.NoError(t, os.Mkdir(target, 0700))
				prefix = filepath.Join(parent, "junction")
				output, err := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", prefix, target).CombinedOutput()
				require.NoError(t, err, "%s", output)
			}
			pathFor := func(leaf string) string {
				path := filepath.Join(prefix, leaf)
				switch name {
				case "relative", "long_relative":
					var err error
					path, err = filepath.Rel(parent, path)
					require.NoError(t, err)
				case "drive_relative":
					path = filepath.VolumeName(parent) + leaf
				case "extended":
					path = `\\?\` + path
				case "unicode":
					path += "-結果-λ"
				case "trailing_separator":
					path += string(os.PathSeparator)
				}
				return path
			}
			control, path := pathFor("control"), pathFor("results")
			require.NoError(t, os.Mkdir(control, 0700), "os.Mkdir control")
			destination, err := prepareBatchOutputDirectory(t.Context(), path)
			require.NoError(t, err)
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.True(t, os.SameFile(destination.info, info))
			payload := []byte("synthetic\r\n\x00\xff")
			_, err = writeBatchDownloadInDirectory(t.Context(), path, "output.jsonl", io.NopCloser(bytes.NewReader(payload)), destination.info)
			require.NoError(t, err)
			_, err = writeBatchDownload(t.Context(), path+string(os.PathSeparator)+"input.jsonl", io.NopCloser(bytes.NewReader(payload)))
			require.NoError(t, err)
			// A relative child of the prepared root must have exactly the saved bytes.
			root, err := os.OpenRoot(path)
			require.NoError(t, err)
			data, err := root.ReadFile("output.jsonl")
			require.NoError(t, err)
			require.Equal(t, payload, data)
			data, err = root.ReadFile("input.jsonl")
			require.NoError(t, err)
			require.Equal(t, payload, data)
			require.NoError(t, root.Close())
			file, err := os.Open(path)
			require.NoError(t, err)
			assertPrivateWindowsDownload(t, file)
			require.NoError(t, file.Close())
		})
	}
}

func TestWindowsBatchOutputDirectoryPreservesPaths(t *testing.T) {
	t.Run("existing", TestBatchOutputDirectoryPreservesExistingPaths)
	t.Run("traversal", TestBatchOutputDirectoryPreservesPathTraversal)
}

func TestWindowsBatchOutputDirectoryWithoutParentListing(t *testing.T) {
	parent := t.TempDir()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	require.NoError(t, err)
	original, err := windows.GetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	require.NoError(t, err)
	setDACL := func(descriptor *windows.SECURITY_DESCRIPTOR) {
		dacl, _, err := descriptor.DACL()
		require.NoError(t, err)
		require.NoError(t, windows.SetNamedSecurityInfo(parent, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil))
	}
	defer setDACL(original)
	sid := user.User.Sid.String()
	descriptor, err := windows.SecurityDescriptorFromString("D:P(D;;0x1;;;" + sid + ")(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	require.NoError(t, err)
	setDACL(descriptor)
	file, err := os.Open(parent)
	if file != nil {
		require.NoError(t, file.Close())
	}
	require.Error(t, err, "the parent must deny listing")
	require.NoError(t, os.Mkdir(filepath.Join(parent, "control"), 0700))
	_, err = prepareBatchOutputDirectory(t.Context(), filepath.Join(parent, "results"))
	require.NoError(t, err)
}
