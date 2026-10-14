//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/windows"
)

type windowsFileReceiptInput struct {
	Shell, Executable, Upload, Source, Control string
}

type windowsFileReceiptResult struct {
	Console                    bool
	UploadExit                 int
	UploadRows, InspectionRows []string
	Printed, Executed          string
	InspectionExit             *int
}

// The actual shell directly starts the production CLI with console handles.
// Console cells combine stdout and stderr; this does not assert byte separation.
func testWindowsFilesConsoleReceipt(t *testing.T, shell, executable, binary, helper string, command func(string, ...string) string) {
	t.Helper()
	work, home, control := t.TempDir(), t.TempDir(), t.TempDir()
	const filename = "upload's copy.txt"
	const project, organization = "project-'quoted value", "org one"
	original := []byte("hello files!\n")
	source := filepath.Join(work, filename)
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	response := fmt.Sprintf(`{"id":%q,"object":"file","bytes":13,"filename":%q,"purpose":"user_data","status":"uploaded"}`, filesWorkflowID, filename)
	upload, uploads := filesWorkflowUploadHandler(t, filename, original, http.StatusOK, response)
	var inspections, downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("OpenAI-Project") != project || r.Header.Get("OpenAI-Organization") != organization {
			t.Errorf("request context changed: project=%q organization=%q", r.Header.Get("OpenAI-Project"), r.Header.Get("OpenAI-Organization"))
		}
		if r.Header.Get("Authorization") != "Bearer sk-fake-windows-receipt" {
			t.Error("unexpected synthetic credential selection")
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/files":
			upload.ServeHTTP(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/files/"+filesWorkflowID:
			inspections.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, response)
		case r.Method == http.MethodGet && r.URL.Path == "/files/"+filesWorkflowID+"/content":
			downloads.Add(1)
			_, _ = w.Write(original)
		default:
			t.Errorf("unexpected receipt request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	spec := windowsFileReceiptInput{Shell: shell, Executable: executable, Source: source, Control: control,
		Upload: command(binary, "files", "upload", filename, "--purpose=user_data", "--project="+project,
			"--organization="+organization, "--base-url="+server.URL)}
	encoded, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	specPath, resultPath := filepath.Join(control, "receipt-input.json"), filepath.Join(control, "console-result.json")
	if err := os.WriteFile(specPath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, helper, "-test.run=^TestShellFileWindowsConsoleHelper$", "--", helper,
		"-test.run=^TestFilesReceiptWindowsConsoleHelper$", "--", specPath)
	process.Dir = work
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE, HideWindow: true}
	process.WaitDelay = 2 * time.Second
	for _, name := range []string{"PATH", "SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"} {
		if value, ok := os.LookupEnv(name); ok {
			process.Env = append(process.Env, name+"="+value)
		}
	}
	process.Env = append(process.Env, "HOME="+home, "USERPROFILE="+home, "APPDATA="+home, "LOCALAPPDATA="+home,
		"TEMP="+home, "TMP="+home, "OPENAI_API_KEY=sk-fake-windows-receipt", "OPENAI_BASE_URL=http://127.0.0.1:1",
		"OPENAI_PROJECT_ID=project-inherited", "OPENAI_ORG_ID=org-inherited", "OPENAI_CLI_TEST_CONSOLE_RESULT="+resultPath,
		"NO_COLOR=1", "FORCE_COLOR=0", "TERM=dumb", "GOMAXPROCS=2")
	var stdout, stderr adminConsoleCapture
	process.Stdout, process.Stderr = &stdout, &stderr
	input, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	// Keep the owner's input open. EOF would request its forced-failure cleanup.
	defer input.Close()
	if err := process.Run(); err != nil || ctx.Err() != nil {
		t.Fatalf("console receipt helper: %v deadline=%v stdout=%q stderr=%q", err, ctx.Err(), stdout.checkedText(t), stderr.checkedText(t))
	}
	if diagnostic := stderr.checkedText(t); diagnostic != "" {
		t.Fatalf("console receipt helper diagnostic: %q", diagnostic)
	}
	var result windowsFileReceiptResult
	if err := json.Unmarshal([]byte(stdout.checkedText(t)), &result); err != nil {
		t.Fatalf("incomplete receipt helper result: %v", err)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var owner windowsConsoleResult
	if err := json.Unmarshal(data, &owner); err != nil {
		t.Fatal(err)
	}
	if !owner.Started || owner.ChildExit == nil || *owner.ChildExit != 0 || owner.EventSent || owner.TimedOut || owner.ForcedKill {
		t.Fatalf("console owner did not finish normally: %+v", owner)
	}
	if !result.Console || result.UploadExit != 0 || uploads.Load() != 1 || downloads.Load() != 0 {
		t.Fatalf("upload contract changed: result=%+v uploads=%d downloads=%d", result, uploads.Load(), downloads.Load())
	}
	expected := []string{"Uploaded " + filename + " (13 B)", "ID: " + filesWorkflowID, "Purpose: user_data"}
	if shell == "pwsh" {
		if result.Printed == "" || result.Executed != result.Printed || result.InspectionExit == nil || *result.InspectionExit != 0 || inspections.Load() != 1 {
			t.Fatalf("printed Inspect command did not finish: result=%+v inspections=%d", result, inspections.Load())
		}
		expected = append(expected, "", "Inspect it: "+result.Printed)
		metadata := strings.Join(result.InspectionRows, "\n")
		if !strings.HasPrefix(metadata, "File metadata\n") || !strings.Contains(metadata, filesWorkflowID) {
			t.Fatalf("Inspect did not show file metadata: %q", metadata)
		}
	} else if result.Printed != "" || result.Executed != "" || result.InspectionExit != nil || inspections.Load() != 0 {
		t.Fatalf("unsupported shell received an Inspect hint: %+v", result)
	}
	if !slices.Equal(result.UploadRows, expected) {
		t.Fatalf("upload receipt rows changed: got=%q expected=%q", result.UploadRows, expected)
	}
	if strings.Contains(result.Printed, "sk-fake-") || strings.Contains(result.Printed, "--output") || strings.Contains(result.Printed, " download ") {
		t.Fatalf("receipt exposed credentials or suggested a write: %q", result.Printed)
	}
	contents, err := os.ReadFile(source)
	if err != nil || string(contents) != "local edits after the upload\n" {
		t.Fatalf("Inspect changed the modified upload source: %q %v", contents, err)
	}
	entries, err := os.ReadDir(work)
	if err != nil || len(entries) != 1 || entries[0].Name() != filename {
		t.Fatalf("receipt workflow created a destination: %v %v", entries, err)
	}
	t.Logf("%s real console upload_exit=%d Inspect_executed=%t uploads=%d inspections=%d downloads=%d rows=%q",
		shell, result.UploadExit, result.InspectionExit != nil, uploads.Load(), inspections.Load(), downloads.Load(), result.UploadRows)
}

// The existing console owner supplies the watchdog and Job Object. This helper
// reads screen cells without changing the test runner's standard handles.
func TestFilesReceiptWindowsConsoleHelper(t *testing.T) {
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		return
	}
	if len(os.Args[separator+1:]) != 1 {
		t.Fatal("invalid receipt helper arguments")
	}
	data, err := os.ReadFile(os.Args[separator+1])
	if err != nil {
		t.Fatal(err)
	}
	var spec windowsFileReceiptInput
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains([]string{"cmd", "powershell", "pwsh"}, spec.Shell) {
		t.Fatal("unsupported receipt test shell")
	}
	input, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	console, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer console.Close()
	if !term.IsTerminal(input.Fd()) || !term.IsTerminal(console.Fd()) {
		t.Fatal("receipt helper lacks real console handles")
	}
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(windows.Handle(console.Fd()), &info); err != nil {
		t.Fatal(err)
	}
	// Keep the printed command on one row. Wrapped commands fail extraction.
	width, height := 2048, max(64, int(info.Window.Bottom)+1)
	if height > 512 {
		t.Fatal("unsupported receipt console height")
	}
	size := uintptr(uint32(width) | uint32(height)<<16)
	resize := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleScreenBufferSize")
	if ok, _, err := resize.Call(console.Fd(), size); ok == 0 {
		t.Fatal(err)
	}
	run := func(script, phase string) (int, []string) {
		clearTokenizerRestorationScreen(t, console)
		args := []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", script + "; exit $LASTEXITCODE"}
		if spec.Shell == "cmd" {
			path := filepath.Join(spec.Control, phase+".cmd")
			// Separate lines preserve the CLI status without early percent expansion.
			if err := os.WriteFile(path, []byte("@echo off\r\n"+script+"\r\nexit /b %errorlevel%\r\n"), 0600); err != nil {
				t.Fatal(err)
			}
			args = []string{"/D", "/C", path}
		}
		child := exec.Command(spec.Executable, args...)
		child.Stdin, child.Stdout, child.Stderr = input, console, console
		code := 0
		if err := child.Run(); err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				t.Fatal(err)
			}
		}
		if err := windows.GetConsoleScreenBufferInfo(windows.Handle(console.Fd()), &info); err != nil || int(info.Size.X) != width {
			t.Fatalf("receipt console geometry changed: %+v %v", info, err)
		}
		screen, err := readOutputConsole(console)
		if err != nil {
			t.Fatal(err)
		}
		cells := utf16.Encode([]rune(screen))
		if len(cells) == 0 || len(cells)%width != 0 || len(cells) > 1<<20 {
			t.Fatal("incomplete receipt console capture")
		}
		var rows []string
		for offset := 0; offset < len(cells); offset += width {
			row := string(utf16.Decode(cells[offset : offset+width]))
			if !strings.HasSuffix(row, " ") {
				t.Fatal("receipt row reached the console boundary")
			}
			rows = append(rows, strings.TrimRight(row, " "))
		}
		for len(rows) > 0 && rows[len(rows)-1] == "" {
			rows = rows[:len(rows)-1]
		}
		return code, rows
	}
	result := windowsFileReceiptResult{Console: true}
	result.UploadExit, result.UploadRows = run(spec.Upload, "upload")
	if result.UploadExit == 0 {
		for _, row := range result.UploadRows {
			if command, ok := strings.CutPrefix(row, "Inspect it: "); ok {
				if result.Printed != "" || command == "" {
					t.Fatal("ambiguous receipt command")
				}
				result.Printed = command
			}
		}
		if err := os.WriteFile(spec.Source, []byte("local edits after the upload\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if spec.Shell == "pwsh" && result.Printed != "" {
			// Execute the extracted command unchanged in the same environment and cwd.
			result.Executed = result.Printed
			code, rows := run(result.Printed, "inspect")
			result.InspectionExit, result.InspectionRows = &code, rows
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}
