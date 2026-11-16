package updatenotice

import (
	"bytes"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDarwinReplacementQuarantine(t *testing.T) {
	for _, tc := range []struct{ name, source, incoming, want string }{
		{"unmarked", "", "", ""}, {"carry existing", "0081;00000000;fixture;", "", "0081;00000000;fixture;"},
		{"incoming marking", "", "0083;00000001;package;", "0083;00000001;package;"},
		{"keep incoming", "0081;00000000;fixture;", "0083;00000001;package;", "0083;00000001;package;"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			image := func(marker string) *os.File {
				f, err := os.CreateTemp(t.TempDir(), "image")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { f.Close() })
				if marker != "" {
					if err := unix.Fsetxattr(int(f.Fd()), quarantineAttribute, []byte(marker), 0); err != nil {
						t.Fatal(err)
					}
				}
				return f
			}
			first, next := image(tc.source), image(tc.incoming)
			if err := preservePreparedImageTrust(first, next); err != nil {
				t.Fatal(err)
			}
			for f, want := range map[*os.File]string{first: tc.source, next: tc.want} {
				data, err := readQuarantine(f)
				if err != nil || (want == "") != (data == nil) || !bytes.Equal(data, []byte(want)) {
					t.Fatal("unexpected image quarantine", err)
				}
			}
		})
	}
}

func TestDarwinRejectsEmptyQuarantine(t *testing.T) {
	first, err := os.CreateTemp(t.TempDir(), "first")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	next, err := os.CreateTemp(t.TempDir(), "next")
	if err != nil {
		t.Fatal(err)
	}
	defer next.Close()
	if err := unix.Fsetxattr(int(first.Fd()), quarantineAttribute, []byte("0081;00000000;fixture;"), 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Fsetxattr(int(next.Fd()), quarantineAttribute, []byte{}, 0); err != nil {
		t.Fatal(err)
	}
	if err := preservePreparedImageTrust(first, next); err == nil {
		t.Fatal("accepted a zero-byte incoming attribute")
	}
}
