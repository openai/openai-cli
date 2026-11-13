package updatenotice

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestHomebrewVerifierSelection(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	bin := filepath.Join(home, ".openai", "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(home, "brew")
	keg := filepath.Join(prefix, "Cellar", "gh", "2.0.0", "bin")
	opt := filepath.Join(prefix, "opt", "gh")
	for _, directory := range []string{keg, filepath.Dir(opt)} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	project := filepath.Join(home, "source")
	if err := os.MkdirAll(filepath.Join(project, "cmd", "gh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module github.com/cli/cli/v2\ngo 1.25\n"), 0600); err != nil {
		t.Fatal(err)
	}
	const source = `package main
import ("fmt"; "os"; "time")
func main() {
 if len(os.Args)>1 && os.Args[1]=="attestation" {
  if len(os.Args)!=17 || os.Args[2]!="verify" || os.Args[3]!="--bundle" || os.Args[13]!="--deny-self-hosted-runners" || os.Args[16]!="--help" { os.Exit(1) }
  if _,err:=os.Stat(os.Getenv("HOME")+"/cold-verifier"); err==nil {time.Sleep(2200*time.Millisecond)}
  fmt.Println("--deny-self-hosted-runners[=true|false]")
  return
 }
 fmt.Println("selected Homebrew fixture")
}
`
	if err := os.WriteFile(filepath.Join(project, "cmd", "gh", "main.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	formula := filepath.Join(keg, "gh")
	build := exec.Command("go", "build", "-o", formula, "./cmd/gh")
	build.Dir = project
	build.Env = append(os.Environ(), "GOPROXY=off", "GOWORK=off")
	if data, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, data)
	}
	if err := os.Symlink("../Cellar/gh/2.0.0", opt); err != nil {
		t.Fatal(err)
	}
	oldDir := filepath.Join(home, "old")
	if err := os.Mkdir(oldDir, 0700); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(oldDir, "gh")
	if err := os.WriteFile(old, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", oldDir)
	if got := archiveVerifierStatus(); got != "unavailable" {
		t.Fatalf("unregistered probe: %s", got)
	}
	if _, err := os.Stat(filepath.Join(bin, ".update")); !os.IsNotExist(err) {
		t.Fatal("unregistered probe created state")
	}
	if got := enrollHomebrewVerifier(t.Context(), prefix); got != "ready" {
		t.Fatalf("enroll: %s", got)
	}
	if got := enrollHomebrewVerifier(t.Context(), prefix); got != "ready" {
		t.Fatalf("repeat: %s", got)
	}
	// A cold macOS signature check can exceed the normal system-gh probe.
	cold := filepath.Join(home, "cold-verifier")
	if err := os.WriteFile(cold, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if got := enrollHomebrewVerifier(t.Context(), prefix); got != "ready" {
		t.Fatalf("cold verifier: %s", got)
	}
	if err := os.Remove(cold); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(filepath.Join(bin, ".update", stateName))
	if err != nil {
		t.Fatal(err)
	}
	if got := archiveVerifierStatus(); got != "ready" {
		t.Fatalf("registered probe: %s", got)
	}
	after, err := os.Stat(filepath.Join(bin, ".update", stateName))
	if err != nil || !os.SameFile(before, after) || before.ModTime() != after.ModTime() {
		t.Fatal("probe changed enrollment")
	}
	cache, err := openCache(t.Context(), filepath.Join(bin, ".update"))
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	state, err := loadUpdateState(cache, time.Now())
	if err != nil || state.HomebrewPrefix != prefix {
		t.Fatalf("receipt: %+v %v", state, err)
	}
	for _, path := range []string{old, ""} {
		d := &releaseDelivery{verifier: path}
		if err := d.checkVerifier(t.Context(), cache); err != nil {
			t.Fatalf("worker failed with %q: %v", path, err)
		}
		selected := d.verifier
		if selected == formula || filepath.Dir(selected) == filepath.Dir(formula) {
			t.Fatal("worker did not pin the selected inode")
		}
		d.closeVerifier()
		if _, err := os.Stat(selected); !os.IsNotExist(err) {
			t.Fatal("snapshot survived session")
		}
	}
	// Simulate a killed installer after creating its private snapshot.
	interrupted := &releaseDelivery{}
	if err := interrupted.openHomebrewVerifier(t.Context(), prefix, cache.directory); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(formula); err != nil || fi.Sys().(*syscall.Stat_t).Nlink != 1 {
		t.Fatal("snapshot changed the source link count")
	}
	if got := archiveVerifierStatus(); got != "ready" {
		t.Fatalf("overlapping probe: %s", got)
	}
	if _, err := os.Stat(interrupted.verifier); err != nil {
		t.Fatal("probe removed another session's snapshot")
	}
	d := &releaseDelivery{verifier: old}
	if err := d.checkVerifier(t.Context(), cache); err != nil {
		t.Fatal(err)
	}
	defer d.closeVerifier()
	latest := filepath.Join(prefix, "Cellar", "gh", "2.0.1", "bin")
	if err := os.MkdirAll(latest, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(formula)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(latest, "gh"), data, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(opt); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../Cellar/gh/2.0.1", opt); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(formula); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(d.verifier).Output(); err != nil || !strings.Contains(string(out), "selected Homebrew") {
		t.Fatalf("original keg snapshot: %v", err)
	}
	d.closeVerifier()
	next := &releaseDelivery{verifier: old}
	if err := next.checkVerifier(t.Context(), cache); err != nil {
		t.Fatal(err)
	}
	next.closeVerifier()
	current := filepath.Join(latest, "gh")
	if err := os.Link(current, filepath.Join(latest, "alias")); err != nil {
		t.Fatal(err)
	}
	if selectEnrolledHomebrewVerifier(t.Context(), next, cache) {
		next.closeVerifier()
		t.Fatal("accepted unexpected hard link")
	}
	if err := os.Remove(filepath.Join(latest, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(latest, 0777); err != nil {
		t.Fatal(err)
	}
	if selectEnrolledHomebrewVerifier(t.Context(), next, cache) {
		next.closeVerifier()
		t.Fatal("accepted a shared keg")
	}
	if err := os.Chmod(latest, 0700); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if selectEnrolledHomebrewVerifier(cancelled, next, cache) {
		next.closeVerifier()
		t.Fatal("accepted cancelled session")
	}
	if paths, _ := filepath.Glob(filepath.Join(cache.directory, ".homebrew-verifier")); len(paths) != 0 {
		t.Fatal("leaked snapshots", paths)
	}
	// A reserved name alone never authorizes deletion of an unrelated file.
	scratch := filepath.Join(cache.directory, ".homebrew-verifier")
	if err := os.Mkdir(scratch, 0700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(scratch, "gh")
	if err := os.WriteFile(keep, []byte("personal"), 0600); err != nil {
		t.Fatal(err)
	}
	if dir, err := prepareHomebrewScratch(cache.directory); err == nil {
		dir.Close()
		t.Fatal("accepted unmarked scratch")
	}
	if b, err := os.ReadFile(keep); err != nil || string(b) != "personal" {
		t.Fatal("changed an unrelated file")
	}
	// Force the cross-volume copy operation and check both its bytes and quarantine.
	attr := []byte("0081;00000000;fixture;")
	if err := unix.Setxattr(current, "com.apple.quarantine", attr, 0); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(current)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	copied := filepath.Join(home, "copied-gh")
	if err := copyHomebrewVerifier(t.Context(), f, copied); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 512)
	n, err := unix.Getxattr(copied, "com.apple.quarantine", got)
	if err != nil || string(got[:n]) != string(attr) {
		t.Fatalf("copy lost quarantine: %v", err)
	}
	if info, err := os.Stat(copied); err != nil || info.Mode().Perm()&0100 == 0 {
		t.Fatal("copy is not executable", err)
	}
	if out, err := exec.Command("/bin/chmod", "+a", "everyone allow write", cache.directory).CombinedOutput(); err != nil {
		t.Fatalf("add fixture ACL: %v %s", err, out)
	}
	defer exec.Command("/bin/chmod", "-N", cache.directory).Run()
	if _, err := loadUpdateState(cache, time.Now()); err == nil {
		t.Fatal("accepted a writable receipt ACL")
	}
}
