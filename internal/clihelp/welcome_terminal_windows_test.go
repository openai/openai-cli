//go:build windows

package clihelp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWelcomeMSYSPipeNames(t *testing.T) {
	const scoped = `\Sessions\2\AppContainerNamedObjects\S-1-15-2-1-2-3-4-5-6-7\`
	for _, name := range []string{
		`\msys-0123456789abcdef-pty0-from-master`,
		`\msys-0123456789ABCDEF-pty12-to-master`,
		`\cygwin-0123456789abcdef-pty0-from-master-nat`,
		`\cygwin-0123456789abcdef-pty0-to-master-cyg`,
		`\Device\NamedPipe\msys-0123456789abcdef-pty0-to-master`,
		scoped + `msys-0123456789abcdef-pty0-from-master`,
		scoped + `cygwin-0123456789abcdef-pty12-to-master-nat`,
		scoped + `cygwin-0123456789abcdef-pty0-to-master-cyg`,
		`\Device\NamedPipe` + scoped + `msys-0123456789ABCDEF-pty0-to-master`,
	} {
		if !welcomePTYName.MatchString(name) {
			t.Errorf("valid PTY name rejected: %q", name)
		}
	}
	for _, name := range []string{
		`\msys--pty0-from-master`, `\msys-nothex-pty0-to-master`,
		`\msys-0123456789abcdef-pty-from-master`, `\msys-0123456789abcdef-ptyA-to-master`,
		`\msys-0123456789abcdef-pipe0-to-master`, `\msys-0123456789abcdef-pty0-to-slave`,
		`\msys-0123456789abcdef-pty0-to-master-extra`, `\msys-0123456789abcdef-pty0-to-master-nat-cyg`,
		`\other-0123456789abcdef-pty0-to-master`, `\tmp\msys-0123456789abcdef-pty0-to-master`,
		`msys-0123456789abcdef-pty0-to-master`, "\\msys-0123456789abcdef-pty0-to-master\x00extra",
		scoped + `ordinary-0123456789abcdef-pty0-to-master`,
		scoped + `msys-nothex-pty0-to-master`,
		scoped + `msys-0123456789abcdef-pty0-to-master-extra`,
		scoped + `nested\msys-0123456789abcdef-pty0-to-master`,
		`\Sessions\x\AppContainerNamedObjects\S-1-15-2-1-2-3-4-5-6-7\msys-0123456789abcdef-pty0-to-master`,
		`\Sessions\2\OtherObjects\S-1-15-2-1-2-3-4-5-6-7\msys-0123456789abcdef-pty0-to-master`,
		`\Sessions\2\AppContainerNamedObjects\S-1-15-3-1-2-3-4-5-6-7\msys-0123456789abcdef-pty0-to-master`,
		`\Sessions\2\AppContainerNamedObjects\S-1-15-2-1-2-3-4-5-6\msys-0123456789abcdef-pty0-to-master`,
		`\Sessions\2\AppContainerNamedObjects\S-1-15-2-1-2-3-4-5-6-x\msys-0123456789abcdef-pty0-to-master`,
	} {
		if welcomePTYName.MatchString(name) {
			t.Errorf("ordinary or malformed pipe name accepted: %q", name)
		}
	}
}

func TestWelcomeMSYSRejectsFilesAndAnonymousPipes(t *testing.T) {
	file, err := os.Create(filepath.Join(t.TempDir(), "msys-0123456789abcdef-pty0-to-master"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	for _, file := range []*os.File{file, reader, writer} {
		if welcomeTerminal(file) {
			t.Errorf("redirected descriptor recognized as a terminal: %s", file.Name())
		}
	}
	if welcomePseudoTerminal(^uintptr(0)) {
		t.Fatal("invalid handle recognized as a PTY")
	}
}
