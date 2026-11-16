package darwinacl

import (
	"encoding/binary"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestDirectoryACLBounds(t *testing.T) {
	deny := make([]byte, 100)
	binary.LittleEndian.PutUint32(deny[0:4], 100)
	binary.LittleEndian.PutUint32(deny[4:8], unix.ATTR_CMN_RETURNED_ATTRS|unix.ATTR_CMN_EXTENDED_SECURITY)
	binary.LittleEndian.PutUint32(deny[24:28], 8)
	binary.LittleEndian.PutUint32(deny[28:32], 68)
	binary.LittleEndian.PutUint32(deny[32:36], 0x012cc16d)
	binary.LittleEndian.PutUint32(deny[68:72], 1)
	binary.LittleEndian.PutUint32(deny[92:96], 2)
	require.NoError(t, check(deny, DenyOnly))
	for _, mutation := range []struct {
		name   string
		offset int
		value  uint32
	}{
		{"truncated header", 0, 31}, {"oversized response", 0, 101},
		{"unknown attribute", 4, 1}, {"unexpected volume attribute", 8, 1},
		{"backwards reference", 24, 0xfffffff8}, {"oversized reference", 28, 69},
		{"bad magic", 32, 0}, {"oversized count", 68, 129}, {"short entry", 68, 2},
		{"deferred inheritance", 72, 1 << 16}, {"unknown ACL flags", 72, 1 << 31},
		{"write grant", 92, 1}, {"unknown entry", 92, 3}, {"unknown entry flags", 92, 2 | 1<<31},
	} {
		t.Run(mutation.name, func(t *testing.T) {
			data := append([]byte(nil), deny...)
			binary.LittleEndian.PutUint32(data[mutation.offset:mutation.offset+4], mutation.value)
			require.Error(t, check(data, DenyOnly))
		})
	}
	require.Error(t, check(deny[:31], DenyOnly))
}

func TestNativeVerifierDirectoryACL(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { _ = exec.Command("/bin/chmod", "-N", dir).Run() })
	f, err := os.Open(dir)
	require.NoError(t, err)
	defer f.Close()
	for _, test := range []struct {
		acl     string
		allowed bool
	}{
		{"", true}, {"everyone deny delete", true}, {"everyone allow readattr", true}, {"everyone allow write", false},
	} {
		require.NoError(t, exec.Command("/bin/chmod", "-N", dir).Run())
		args := []string{"-N", dir}
		if test.acl != "" {
			args = []string{"+a", test.acl, dir}
		}
		out, err := exec.Command("/bin/chmod", args...).CombinedOutput()
		require.NoError(t, err, string(out))
		require.Equal(t, test.allowed, Check(f, ReadOnly) == nil, test.acl)
	}
	root, err := os.Open("/")
	require.NoError(t, err)
	defer root.Close()
	require.NoError(t, Check(root, ReadOnly))
}
