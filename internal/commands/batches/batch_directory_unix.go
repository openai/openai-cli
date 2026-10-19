//go:build !windows

package batches

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const batchDirectoryHelperArgument = "__batch-directory"

// RunBatchDirectoryHelper creates a directory without changing the parent's
// umask or changing permissions through a replaceable pathname.
func RunBatchDirectoryHelper(args []string) (bool, error) {
	if len(args) < 2 || args[1] != batchDirectoryHelperArgument {
		return false, nil
	}
	if len(args) != 4 || args[2] == "" || args[3] != "3" {
		return true, errors.New("invalid batch directory helper invocation")
	}
	life := os.NewFile(3, "batch-directory-lifeline")
	if life == nil {
		return true, errors.New("missing batch directory lifeline")
	}
	defer life.Close()
	var lifelineInfo os.FileInfo
	for _, descriptor := range []struct {
		file *os.File
		mode int
	}{{life, unix.O_RDONLY}, {os.Stdout, unix.O_WRONLY}} {
		info, err := descriptor.file.Stat()
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
			return true, errors.New("batch directory helper requires pipes")
		}
		if lifelineInfo == nil {
			lifelineInfo = info
		} else if os.SameFile(lifelineInfo, info) {
			return true, errors.New("batch directory pipes must be distinct")
		}
		flags, err := unix.FcntlInt(descriptor.file.Fd(), unix.F_GETFL, 0)
		if err != nil || flags&unix.O_ACCMODE != descriptor.mode {
			return true, errors.New("invalid batch directory pipe access")
		}
	}
	// The parent context owns signal handling. Its lifeline also stops this
	// helper if the parent disappears during a blocked filesystem operation.
	signal.Ignore(batchOperationSignals...)
	finished := make(chan struct{})
	defer close(finished) // Close before life so normal return cannot look like parent loss.
	granted := make(chan struct{})
	go func() {
		var value [1]byte
		_, err := io.ReadFull(life, value[:])
		if err == nil && value[0] == 'G' {
			close(granted)
			_, _ = life.Read(value[:])
		}
		select {
		case <-finished:
			return
		default:
		}
		os.Exit(1)
	}()
	if _, err := os.Stdout.Write([]byte{'R'}); err != nil {
		return true, err
	}
	// Opposite pipe ends can have different stat identities. Require a parent
	// grant before mkdir; aliased reply/lifeline pipes consume R instead of G.
	<-granted
	previous := syscall.Umask(0077)
	err := os.Mkdir(args[2], 0700)
	syscall.Umask(previous)
	var errno syscall.Errno
	if err != nil && !errors.As(err, &errno) {
		return true, errors.New("batch directory creation returned no errno")
	}
	var response [8]byte
	binary.LittleEndian.PutUint64(response[:], uint64(errno))
	_, err = os.Stdout.Write(response[:])
	// A transported mkdir error is a successful helper exchange. Process
	// failures must never be mistaken for an existing-directory result.
	return true, err
}

func createPrivateBatchDirectory(ctx context.Context, path string) (os.FileInfo, error) {
	if err := runBatchDirectoryHelper(ctx, path); err != nil {
		return nil, err
	}
	// This observes the selected destination, not ownership of the mkdir inode.
	// Never chmod or remove a directory based on this later snapshot.
	observed, err := os.Lstat(path)
	if err == nil && !observed.IsDir() {
		err = errors.New("new output directory changed")
	}
	return observed, err
}

func runBatchDirectoryHelper(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	replyRead, replyWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer replyRead.Close()
	defer replyWrite.Close()
	lifeRead, lifeWrite, err := os.Pipe()
	if err != nil {
		return err
	}
	defer lifeRead.Close()
	defer lifeWrite.Close()
	workerContext, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	command := exec.CommandContext(workerContext, executable, batchDirectoryHelperArgument, path)
	command.Stdout = replyWrite
	command.Env = []string{"GOMAXPROCS=" + strconv.Itoa(runtime.GOMAXPROCS(0))}
	command.WaitDelay = 2 * time.Second
	if err := inheritBatchOutputLifeline(command, lifeRead); err != nil {
		return err
	}
	stopped := make(chan struct{})
	stopCancellation := context.AfterFunc(workerContext, func() {
		defer close(stopped)
		_ = replyRead.Close()
		_ = lifeWrite.Close()
	})
	defer func() {
		stopWorker()
		if !stopCancellation() {
			<-stopped
		}
	}()
	if err := command.Start(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	waited := false
	wait := func() error {
		waited = true
		return command.Wait()
	}
	defer func() {
		stopWorker()
		if !waited {
			_ = wait()
		}
	}()
	_ = replyWrite.Close()
	_ = lifeRead.Close()
	startup := time.AfterFunc(2*time.Second, stopWorker)
	if err := grantBatchDirectoryOperation(workerContext, startup, replyRead, lifeWrite); err != nil {
		stopWorker()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	// Waiting for mkdir follows the Batch context, with no new filesystem
	// deadline. The first result byte starts a bounded protocol/exit phase.
	var response [8]byte
	_, resultErr := io.ReadFull(replyRead, response[:1])
	completion := time.AfterFunc(2*time.Second, stopWorker)
	defer completion.Stop()
	if resultErr == nil {
		_, resultErr = io.ReadFull(replyRead, response[1:])
	}
	var extra [1]byte
	if resultErr == nil {
		n, endErr := replyRead.Read(extra[:])
		if n != 0 || endErr != io.EOF {
			resultErr = errors.New("invalid batch directory helper result")
		}
	}
	if resultErr != nil {
		stopWorker()
	}
	waitErr := wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if resultErr != nil || waitErr != nil || workerContext.Err() != nil {
		return errors.New("batch directory helper did not finish successfully")
	}
	value := binary.LittleEndian.Uint64(response[:])
	if value > uint64(^uintptr(0)>>1) {
		return errors.New("invalid batch directory helper errno")
	}
	if value != 0 {
		return &os.PathError{Op: "mkdir", Path: path, Err: syscall.Errno(value)}
	}
	return nil
}

func grantBatchDirectoryOperation(ctx context.Context, startup *time.Timer, reply io.Reader, life io.Writer) error {
	var ready [1]byte
	_, err := io.ReadFull(reply, ready[:])
	// Stop must win before granting mkdir. A fired callback may not have
	// canceled ctx yet, so checking ctx alone cannot end the startup phase.
	stopped := startup.Stop()
	if err != nil || ready[0] != 'R' || !stopped || ctx.Err() != nil {
		return errors.New("batch directory helper did not become ready")
	}
	if _, err := life.Write([]byte{'G'}); err != nil || ctx.Err() != nil {
		return errors.New("batch directory helper did not become ready")
	}
	return nil
}
