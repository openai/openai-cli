package output

import (
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"
)

// Stream writes to process stdout or its pager according to terminal state.
func Stream(label string, generateOutput func(w *os.File) error) error {
	// For non-tty output (probably a pipe), write directly to stdout
	if !term.IsTerminal(os.Stdout.Fd()) {
		return StreamToStdout(generateOutput)
	}

	// When streaming output on Unix-like systems, there's a special trick involving creating two socket pairs
	// that we prefer because it supports small buffer sizes which results in less pagination per buffer. The
	// constructs needed to run it don't exist on Windows builds, so we have this function broken up into
	// OS-specific files with conditional build comments. Under Windows (and in case our fancy constructs fail
	// on Unix), we fall back to using pipes (`StreamToPagerWithPipe`), which are OS agnostic.
	//
	// Defined in either pager_unix.go or pager_windows.go.
	return StreamToPager(label, generateOutput)
}

// pagerCommand splits PAGER into an executable and its arguments. $PAGER is written
// as a command line by the tools this CLI is used beside (git, gh, man), so
// "less -R" has to reach less rather than be resolved as a single executable name.
// When the first word does not resolve, the whole value is kept, so a configuration
// that works today keeps working, including an executable path that contains spaces.
func pagerCommand() []string {
	pager := strings.TrimSpace(os.Getenv("PAGER"))
	if pager == "" {
		return []string{"less"}
	}
	// Prefer a complete executable path before treating spaces as argument
	// separators; a path containing spaces may itself be executable.
	if _, err := exec.LookPath(pager); err == nil {
		return []string{pager}
	}
	if command := strings.Fields(pager); len(command) > 1 {
		if _, err := exec.LookPath(command[0]); err == nil {
			return command
		}
	}
	return []string{pager}
}

// PagerError distinguishes pager setup and process failures from the request,
// formatter, or output callback. Keep the cause available to errors.Is/As.
type PagerError struct{ error }

func (e *PagerError) Unwrap() error { return e.error }

func wrapPagerError(err error) error {
	if err == nil {
		return nil
	}
	return &PagerError{err}
}

// StreamToPagerWithPipe runs the pipe-based pager transport used on Windows
// and when the Unix socket transport cannot be established.
func StreamToPagerWithPipe(label string, generateOutput func(w *os.File) error) error {
	r, w, err := os.Pipe()
	if err != nil {
		return wrapPagerError(err)
	}
	defer r.Close()
	defer w.Close()

	command := pagerCommand()
	if _, err := exec.LookPath(command[0]); err != nil {
		return wrapPagerError(err)
	}

	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = r
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"LESS=-X -r -P "+label,
		"MORE=-r -P "+label,
	)

	if err := cmd.Start(); err != nil {
		return wrapPagerError(err)
	}

	if err := r.Close(); err != nil {
		return wrapPagerError(err)
	}

	// If we would be streaming to a terminal and aren't forcing color one way
	// or the other, we should configure things to use color so the pager gets
	// colorized input.
	if term.IsTerminal(os.Stdout.Fd()) && os.Getenv("FORCE_COLOR") == "" {
		os.Setenv("FORCE_COLOR", "1")
	}

	outputErr := generateOutput(w)
	// Deliver EOF and reap the pager even when output generation failed.
	w.Close()
	waitErr := cmd.Wait()
	if outputErr != nil && !IsBrokenPipe(outputErr) {
		return outputErr
	}
	return wrapPagerError(waitErr)
}

// StreamToStdout preserves stdout signal handling and suppresses only failures
// classified as broken output pipes, retaining upstream and diagnostic errors.
func StreamToStdout(generateOutput func(w *os.File) error) error {
	signal.Ignore(syscall.SIGPIPE)
	err := generateOutput(os.Stdout)
	if IsBrokenPipe(err) {
		return nil
	}
	return err
}
