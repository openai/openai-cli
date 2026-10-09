package custom

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdminSetupKeyRejectsNonTerminal(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "not-a-terminal")
	require.NoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	for _, files := range [][2]*os.File{{nil, file}, {file, nil}, {file, file}} {
		key, err := readAdminSetupKey(context.Background(), files[0], files[1])
		require.ErrorIs(t, err, errAdminSetupTerminal)
		require.Nil(t, key)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	key, err := readAdminSetupKey(ctx, file, file)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, key)
	content, err := os.ReadFile(file.Name())
	require.NoError(t, err)
	require.Empty(t, content)
}

func TestAdminSetupKeyRejectsUnsafePaste(t *testing.T) {
	for _, content := range []string{"synthetic key", "synthetic\nkey", "synthetic\rkey", "synthetic\tkey", "synthetic\x00key", "synthetic\x1bkey", "synthetic\u00a0key"} {
		t.Run(strconv.QuoteToASCII(content), func(t *testing.T) {
			chunks := make(chan []byte, 1)
			input := []byte("\x1b[200~" + content + "\x1b[201~\r")
			chunks <- input
			close(chunks)
			key, err := collectAdminSetupKey(context.Background(), chunks)
			require.ErrorIs(t, err, errAdminSetupKeyInvalid)
			require.Nil(t, key)
			require.Equal(t, make([]byte, len(input)), input, "input buffer must be cleared")
		})
	}
}

func TestAdminSetupKeyChunkBoundaries(t *testing.T) {
	const input = "\x1b[200~synthetic-admin-key\x1b[201~\r"
	for split := 1; split < len(input); split++ {
		t.Run(strconv.Itoa(split), func(t *testing.T) {
			first, second := []byte(input[:split]), []byte(input[split:])
			chunks := make(chan []byte, 2)
			chunks <- first
			chunks <- second
			close(chunks)
			key, err := collectAdminSetupKey(context.Background(), chunks)
			require.NoError(t, err)
			require.True(t, string(key) == "synthetic-admin-key", "entered key differs")
			clear(key)
			require.Equal(t, make([]byte, len(first)), first)
			require.Equal(t, make([]byte, len(second)), second)
		})
	}
}

type adminSetupQueuedInputReader struct {
	adminSetupKeyReader
	control   *os.File
	active    atomic.Int64
	started   atomic.Int64
	completed atomic.Int64
	canceled  atomic.Int64
}

func (reader *adminSetupQueuedInputReader) Read(buffer []byte) (int, error) {
	reader.active.Add(1)
	reader.started.Add(1)
	defer func() { reader.completed.Add(1); reader.active.Add(-1) }()
	return reader.adminSetupKeyReader.Read(buffer)
}

func (reader *adminSetupQueuedInputReader) Cancel() bool {
	reader.canceled.Add(1)
	return reader.adminSetupKeyReader.Cancel()
}

func (reader *adminSetupQueuedInputReader) Close() (err error) {
	defer func() { err = errors.Join(err, reader.adminSetupKeyReader.Close()) }()
	if reader.active.Load() != 0 || reader.started.Load() == 0 ||
		reader.started.Load() != reader.completed.Load() || reader.canceled.Load() != 1 {
		return errors.New("key reader was not joined before Close")
	}
	if err := reader.control.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(os.Stdout, "ADMIN_KEY_READ_JOINED"); err != nil {
		return err
	}
	var release [1]byte
	if _, err := io.ReadFull(reader.control, release[:]); err != nil {
		return err
	}
	if release[0] != 'q' {
		return errors.New("key reader cleanup handshake failed")
	}
	return nil
}

func TestAdminSetupKeyTerminal(t *testing.T) {
	if scenario := os.Getenv("OPENAI_ADMIN_SETUP_KEY_TEST_CASE"); scenario != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		output := os.Stderr
		queued := scenario == "queued_success" || scenario == "queued_ctrl_c"
		var control *os.File
		if fd := os.Getenv("OPENAI_ADMIN_SETUP_KEY_TEST_FD"); fd != "" {
			value, err := strconv.Atoi(fd)
			require.NoError(t, err)
			control = os.NewFile(uintptr(value), "admin-key-test-control")
			defer control.Close()
			if scenario == "output_failure" {
				output = control
			} else if !queued {
				go func() {
					var b [1]byte
					_, _ = control.Read(b[:])
					cancel()
				}()
			}
		}
		if queued {
			require.NotNil(t, control)
		}
		count := 1
		if scenario == "repeated" {
			count = 2
		}
		for range count {
			newReader := newAdminSetupKeyReader
			if queued {
				newReader = func(input io.Reader) (adminSetupKeyReader, error) {
					reader, err := newAdminSetupKeyReader(input)
					if err != nil {
						return nil, err
					}
					return &adminSetupQueuedInputReader{adminSetupKeyReader: reader, control: control}, nil
				}
			}
			key, err := readAdminSetupKeyWithReader(ctx, os.Stdin, output, newReader)
			switch scenario {
			case "ctrl_c", "ctrl_d", "context", "context_paste", "paste_ctrl_c", "paste_ctrl_d", "queued_ctrl_c":
				require.ErrorIs(t, err, context.Canceled)
				require.Nil(t, key)
			case "empty":
				require.ErrorIs(t, err, errAdminSetupKeyEmpty)
				require.Nil(t, key)
			case "space", "multiline", "multiline_large", "tab", "paste_escape", "paste_invalid_utf8":
				require.ErrorIs(t, err, errAdminSetupKeyInvalid)
				require.Nil(t, key)
			case "output_failure":
				require.ErrorIs(t, err, errAdminSetupKeyIO)
				require.Nil(t, key)
			default:
				require.NoError(t, err)
				want := "synthetic-admin-key"
				if scenario == "long_paste" {
					want = strings.Repeat("synthetic", 8192)
				}
				// Avoid printing the returned credential even when a check fails.
				require.True(t, string(key) == want, "entered key differs")
				clear(key)
			}
			if queued {
				_, err := os.Stdin.Stat()
				require.NoError(t, err, "shared stdin must remain open")
			}
		}
		fmt.Fprintln(os.Stdout, "ADMIN_KEY_TEST_DONE")
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix PTY; Windows console execution needs a native host")
	}
	python, err := exec.LookPath("python3")
	if errors.Is(err, exec.ErrNotFound) {
		t.Skip("python3 is needed for the PTY regression")
	}
	require.NoError(t, err)
	const script = `import fcntl,os,pty,select,struct,subprocess,sys,termios,time
scenarios=['typed','paste','backspace','empty','space','tab','multiline','multiline_large','long_paste','ctrl_c','ctrl_d','context','context_paste','output_failure','repeated','paste_ctrl_c','paste_ctrl_d','paste_escape','paste_invalid_utf8']
if sys.platform in ['darwin','linux']:
    scenarios+=['queued_success','queued_ctrl_c']
if os.environ.get('OPENAI_ADMIN_SETUP_KEY_PARENT_CASES'):
    scenarios=os.environ['OPENAI_ADMIN_SETUP_KEY_PARENT_CASES'].split(',')
def queued_bytes(fd):
    return struct.unpack('i',fcntl.ioctl(fd,termios.FIONREAD,struct.pack('i',0)))[0]
for scenario in scenarios:
    master,slave=pty.openpty()
    initial=termios.tcgetattr(slave)
    control,writer=os.pipe()
    queued=scenario in ['queued_success','queued_ctrl_c']
    if queued:
        # Go deadlines require a pollable inherited control descriptor.
        os.set_blocking(control,False)
    os.set_blocking(writer,False)
    inherited=control
    if scenario=='output_failure':
        inherited=os.open(os.ttyname(slave),os.O_RDONLY)
    env={k:v for k,v in os.environ.items() if not k.startswith('OPENAI_')}
    env.update(TERM='xterm-256color',OPENAI_ADMIN_SETUP_KEY_TEST_CASE=scenario,OPENAI_ADMIN_SETUP_KEY_TEST_FD=str(inherited))
    child=None
    raw=bytearray()
    prompts=0
    pending=b''
    pending_time=0
    deadline=time.monotonic()+15
    tail=b'synthetic-queued-tail\nSYNTHETIC_SENTINEL_DATA_ONLY\n'
    queue_boundary=False
    queue_proved=False
    queue_deadline=deadline
    try:
        child=subprocess.Popen([sys.argv[1],'-test.run=^TestAdminSetupKeyTerminal$','-test.timeout=20s'],stdin=slave,stdout=slave,stderr=slave,env=env,pass_fds=(inherited,))
        while child.poll() is None:
            assert time.monotonic()<deadline,scenario+': prompt did not terminate'
            if select.select([master],[],[],.01)[0]:
                raw.extend(os.read(master,65536))
                assert len(raw)<=65536,scenario+': terminal capture exceeded its bound'
            visible=raw.count(b'Admin API key (hidden): ')
            if visible>prompts:
                prompts=visible
                assert not termios.tcgetattr(slave)[3]&(termios.ICANON|termios.ECHO),scenario+': prompt appeared before no-echo'
                data={
                    'typed':b'synthetic-admin-key\r',
                    'queued_success':b'synthetic-admin-key\r',
                    'queued_ctrl_c':b'\x1b[200~synthetic-incomplete-paste\x03',
                    'paste':b'\x1b[200~synthetic-admin-key\x1b[201~\r',
                    'backspace':b'synthetic-admin-kez\x7fy\r',
                    'empty':b'\r',
                    'space':b'synthetic key\r',
                    'tab':b'synthetic\tkey\r',
                    'ctrl_c':b'synthetic-before-cancel\x03',
                    'ctrl_d':b'synthetic-before-cancel\x04',
                    'repeated':b'synthetic-admin-key\r',
                    'context':b'synthetic-before-context',
                    'context_paste':b'\x1b[200~synthetic-incomplete-paste',
                    'paste_ctrl_c':b'\x1b[200~synthetic-incomplete-paste\x03',
                    'paste_ctrl_d':b'\x1b[200~synthetic-incomplete-paste\x04',
                    'paste_escape':b'\x1b[200~synthetic\x1b[31m-admin-key\x1b[201~\r',
                    'paste_invalid_utf8':b'\x1b[200~synthetic\xff-admin-key\x1b[201~\r',
                    'multiline':b'\x1b[200~synthetic-first\n',
                    'multiline_large':b'\x1b[200~synthetic-first\n',
                    'long_paste':b'\x1b[200~'+b'synthetic'*8192+b'\x1b[201~\r',
                }[scenario]
                # Nonblocking writes keep long-paste coverage from hanging the harness.
                pending=data
                os.set_blocking(master,False)
                pending_time=time.monotonic()
            if queued and not queue_boundary and b'ADMIN_KEY_READ_JOINED' in raw:
                assert not pending,scenario+': initial input was not fully written'
                assert not termios.tcgetattr(slave)[3]&(termios.ICANON|termios.ECHO),scenario+': cleanup restored input too early'
                assert queued_bytes(slave)==0,scenario+': initial input remains pending'
                assert len(tail)<=512
                queue_boundary=True
                queue_deadline=time.monotonic()+2
                pending=tail
                pending_time=0
            if pending and select.select([],[master],[],0)[1]:
                try:
                    n=os.write(master,pending)
                    pending=pending[n:]
                except BlockingIOError:
                    pass
            if queue_boundary and not queue_proved:
                assert time.monotonic()<queue_deadline,scenario+': queued-input handshake timed out'
                assert not termios.tcgetattr(slave)[3]&(termios.ICANON|termios.ECHO),scenario+': queued input is not hidden'
                if not pending and queued_bytes(slave)==len(tail):
                    # Only release Close after the real terminal reports the full tail.
                    assert os.write(writer,b'q')==1
                    queue_proved=True
            if prompts and not pending and pending_time and time.monotonic()-pending_time>.15:
                pending_time=0
                if scenario in ['context','context_paste']:
                    os.write(writer,b'x')
                elif scenario in ['multiline','multiline_large']:
                    assert child.poll() is None,scenario+': pasted newline submitted prematurely'
                    suffix=b'synthetic-tail' if scenario=='multiline' else b'synthetic-tail'*4096
                    pending=suffix+b'\x1b[201~\r'
        child.wait(timeout=1)
        remaining=queued_bytes(slave) if queued else 0
        drain_deadline=time.monotonic()+1
        while select.select([master],[],[],.03)[0]:
            assert time.monotonic()<drain_deadline,scenario+': output drain timed out'
            raw.extend(os.read(master,65536))
            assert len(raw)<=65536,scenario+': terminal capture exceeded its bound'
        assert child.returncode==0,(scenario,bytes(raw))
        assert b'ADMIN_KEY_TEST_DONE' in raw,(scenario,bytes(raw))
        assert b'synthetic' not in raw,scenario+': synthetic input appeared in terminal output'
        assert b'SYNTHETIC_SENTINEL_DATA_ONLY' not in raw,scenario+': queued sentinel appeared in terminal output'
        if queued:
            assert queue_boundary and queue_proved,scenario+': queued input was not proven before cleanup'
            assert remaining==0,scenario+': input remains after helper cleanup'
        restored=termios.tcgetattr(slave)
        if sys.platform=='darwin':
            initial[3]&=~termios.PENDIN
            restored[3]&=~termios.PENDIN
        assert restored==initial,(scenario,initial,restored)
        if scenario!='output_failure':
            expected=2 if scenario=='repeated' else 1
            assert prompts==expected,(scenario,prompts)
            assert raw.count(b'\x1b[?2004h')==expected,(scenario,bytes(raw))
            assert raw.count(b'\x1b[?2004l')==expected,(scenario,bytes(raw))
        print(scenario+': PASS')
    finally:
        try:
            if child is not None:
                if child.poll() is None:
                    child.kill()
                child.wait(timeout=2)
        finally:
            if inherited!=control:
                os.close(inherited)
            for fd in [control,writer,master,slave]:
                os.close(fd)
`
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-I", "-B", "-c", script, os.Args[0])
	command.WaitDelay = 2 * time.Second
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Logf("%s", output)
}
