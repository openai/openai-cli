package updatenotice

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifierWindowsAcquisitionPins(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install-windows-release.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "$ghVersion = '"+verifierVersion+"'") {
		t.Fatal("the Windows acquisition verifier version differs from the updater")
	}
	for _, arch := range []string{"amd64", "arm64", "386"} {
		pin := verifierPins["windows_"+arch]
		if !strings.Contains(text, fmt.Sprintf("'%s' = @('%s', '%s')", arch, pin.archive, pin.binary)) {
			t.Errorf("the Windows %s acquisition pins differ from the updater", arch)
		}
	}
}
