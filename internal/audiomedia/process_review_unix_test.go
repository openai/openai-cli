//go:build !windows

package audiomedia

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"syscall"
	"testing"
	"time"
)

type audioReviewDescendantReady struct {
	PID    int `json:"pid"`
	Leader int `json:"leader"`
	Group  int `json:"group"`
}

// This fixture creates only test-owned descendants. The readiness connection
// also provides a cleanup lifeline if the production cleanup assertion fails.
func TestAudioReviewDescendantProcess(t *testing.T) {
	marker := slices.Index(os.Args, "--")
	if marker < 0 || len(os.Args) < marker+3 {
		return
	}
	args := os.Args[marker+1:]
	if args[0] != "audio-review-parent" && args[0] != "audio-review-child" {
		return
	}
	runtime.GOMAXPROCS(2)
	address := args[1]
	if args[0] == "audio-review-child" {
		connection, err := net.DialTimeout("tcp", address, 2*time.Second)
		if err != nil {
			os.Exit(30)
		}
		group, err := syscall.Getpgid(0)
		if err != nil {
			os.Exit(31)
		}
		ready := audioReviewDescendantReady{PID: os.Getpid(), Leader: os.Getppid(), Group: group}
		if err := json.NewEncoder(connection).Encode(ready); err != nil {
			os.Exit(32)
		}
		parentReady := os.NewFile(3, "owned-descendant-ready")
		if parentReady == nil {
			os.Exit(33)
		}
		if _, err := parentReady.Write([]byte{1}); err != nil {
			os.Exit(34)
		}
		if err := parentReady.Close(); err != nil {
			os.Exit(35)
		}
		// The test sends no data. Production termination should close this
		// connection before teardown. Closing it on failure releases this child.
		_ = connection.SetReadDeadline(time.Now().Add(8 * time.Second))
		_, _ = connection.Read(make([]byte, 1))
		_ = connection.Close()
		os.Exit(0)
	}
	if len(args) != 3 {
		os.Exit(36)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		os.Exit(37)
	}
	read, write, err := os.Pipe()
	if err != nil {
		os.Exit(38)
	}
	executable, err := os.Executable()
	if err != nil {
		os.Exit(39)
	}
	child := exec.Command(executable, "-test.run=^TestAudioReviewDescendantProcess$", "--", "audio-review-child", address)
	child.Env = append(helperEnvironment(), "GOMAXPROCS=2")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	child.ExtraFiles = []*os.File{write}
	if err := child.Start(); err != nil {
		os.Exit(40)
	}
	_ = write.Close()
	var ready [1]byte
	if _, err := io.ReadFull(read, ready[:]); err != nil {
		_ = child.Process.Kill()
		_ = child.Wait()
		os.Exit(41)
	}
	_ = read.Close()
	if args[2] == "ffmpeg" {
		if _, err := os.Stdout.Write(testWave(1, 1, 16, 24000, []byte{0, 128})); err != nil {
			os.Exit(42)
		}
	}
	// Deliberately exit with a live descendant holding stdout/stderr open.
	// The production process-group cleanup owns its termination.
	os.Exit(0)
}

func TestAudioReviewCompletedHelpersReleaseDescendantPipes(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"analysis", "playback"} {
		t.Run(operation, func(t *testing.T) {
			for iteration := range 2 {
				t.Run(string(rune('1'+iteration)), func(t *testing.T) {
					listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
					if err != nil {
						t.Fatal(err)
					}
					defer listener.Close()
					ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
					defer cancel()
					h := helpers{
						find: func(name string) (string, error) { return name, nil },
						command: func(ctx context.Context, name string, _ ...string) *exec.Cmd {
							return exec.CommandContext(ctx, executable, "-test.run=^TestAudioReviewDescendantProcess$", "--", "audio-review-parent", listener.Addr().String(), name)
						},
					}
					file := testFile(t, []byte{0, 128})
					completed := make(chan error, 1)
					go func() {
						defer close(completed)
						if operation == "analysis" {
							_, err := analyze(ctx, file, "mp3", h)
							completed <- err
						} else {
							completed <- play(ctx, file, "pcm", 0, h)
						}
					}()
					defer func() {
						cancel()
						select {
						case <-completed:
						case <-time.After(2 * time.Second):
							t.Error("owned helper did not finish during test cleanup")
						}
					}()
					if err := listener.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
						t.Fatal(err)
					}
					connection, err := listener.AcceptTCP()
					if err != nil {
						cancel()
						select {
						case operationErr := <-completed:
							t.Fatalf("descendant readiness failed: %v; operation: %v", err, operationErr)
						case <-time.After(2 * time.Second):
							t.Fatalf("descendant readiness failed and cleanup stalled: %v", err)
						}
					}
					defer connection.Close()
					if err := connection.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
						t.Fatal(err)
					}
					var ready audioReviewDescendantReady
					if err := json.NewDecoder(connection).Decode(&ready); err != nil {
						t.Fatal(err)
					}
					if ready.Leader <= 1 || ready.PID == ready.Leader || ready.Group != ready.Leader {
						t.Fatalf("fixture did not join its owned helper group: %+v", ready)
					}
					select {
					case err := <-completed:
						want := ErrAnalysisFailed
						if operation == "playback" {
							want = ErrPlaybackFailed
						}
						if !errors.Is(err, want) || ctx.Err() != nil {
							t.Fatalf("inherited pipes did not produce a bounded helper failure: %v", err)
						}
					case <-time.After(2 * time.Second):
						t.Fatal("helper completion waited for an inherited pipe indefinitely")
					}
					var byteAfterExit [1]byte
					if n, err := connection.Read(byteAfterExit[:]); n != 0 || err != io.EOF {
						t.Fatalf("owned descendant survived helper cleanup: read=%d error=%v", n, err)
					}
				})
			}
		})
	}
}
