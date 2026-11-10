package terminalimage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFontRegistrationOutlivesTemporaryHome(t *testing.T) {
	host := t.TempDir()
	cache := t.TempDir()
	const session = "abc"
	direct, err := fontGalleryDirectory(host, session)
	if err != nil || direct != filepath.Join(host, "openai", "image-terminal", session) {
		t.Fatalf("fresh native cache: %q, %v", direct, err)
	}
	if err := os.Symlink(filepath.Join(host, "openai"), filepath.Join(cache, "openai")); err != nil {
		t.Fatal(err)
	}
	demo, err := fontGalleryDirectory(cache, session)
	if err != nil || demo != direct {
		t.Fatalf("registration URL differs between demo and regular terminal: %q, %q, %v", demo, direct, err)
	}
	if err := os.MkdirAll(demo, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cache); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(demo); err != nil {
		t.Fatalf("registration path disappeared with the demo home: %v", err)
	}
}
