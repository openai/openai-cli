package custom

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"

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

func TestAdminSetupKeyTerminal(t *testing.T) {
	if scenario := os.Getenv("OPENAI_ADMIN_SETUP_KEY_TEST_CASE"); scenario != "" {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		output := os.Stderr
		if fd := os.Getenv("OPENAI_ADMIN_SETUP_KEY_TEST_FD"); fd != "" {
			value, err := strconv.Atoi(fd)
			require.NoError(t, err)
			control := os.NewFile(uintptr(value), "admin-key-test-control")
			defer control.Close()
			if scenario == "output_failure" {
				output = control
			} else {
				go func() {
					var b [1]byte
					_, _ = control.Read(b[:])
					cancel()
				}()
			}
		}
		count := 1
		if scenario == "repeated" {
			count = 2
		}
		for range count {
			key, err := readAdminSetupKey(ctx, os.Stdin, output)
			switch scenario {
			case "ctrl_c", "ctrl_d", "context", "context_paste", "paste_ctrl_c", "paste_ctrl_d":
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
	const script = `import os,pty,select,subprocess,sys,termios,time
scenarios=['typed','paste','backspace','empty','space','tab','multiline','multiline_large','long_paste','ctrl_c','ctrl_d','context','context_paste','output_failure','repeated','paste_ctrl_c','paste_ctrl_d','paste_escape','paste_invalid_utf8']
if os.environ.get('OPENAI_ADMIN_SETUP_KEY_PARENT_CASES'):
    scenarios=os.environ['OPENAI_ADMIN_SETUP_KEY_PARENT_CASES'].split(',')
for scenario in scenarios:
    master,slave=pty.openpty()
    initial=termios.tcgetattr(slave)
    control,writer=os.pipe()
    inherited=control
    if scenario=='output_failure':
        inherited=os.open(os.ttyname(slave),os.O_RDONLY)
    env={k:v for k,v in os.environ.items() if not k.startswith('OPENAI_')}
    env.update(TERM='xterm-256color',OPENAI_ADMIN_SETUP_KEY_TEST_CASE=scenario,OPENAI_ADMIN_SETUP_KEY_TEST_FD=str(inherited))
    child=subprocess.Popen([sys.argv[1],'-test.run=^TestAdminSetupKeyTerminal$'],stdin=slave,stdout=slave,stderr=slave,env=env,pass_fds=(inherited,))
    raw=bytearray()
    prompts=0
    pending=b''
    pending_time=0
    deadline=time.monotonic()+15
    try:
        while child.poll() is None:
            assert time.monotonic()<deadline,scenario+': prompt did not terminate'
            if select.select([master],[],[],.01)[0]:
                raw.extend(os.read(master,65536))
            visible=raw.count(b'Admin API key (hidden): ')
            if visible>prompts:
                prompts=visible
                assert not termios.tcgetattr(slave)[3]&(termios.ICANON|termios.ECHO),scenario+': prompt appeared before no-echo'
                data={
                    'typed':b'synthetic-admin-key\r',
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
            if pending and select.select([],[master],[],0)[1]:
                try:
                    n=os.write(master,pending)
                    pending=pending[n:]
                except BlockingIOError:
                    pass
            if prompts and not pending and pending_time and time.monotonic()-pending_time>.15:
                pending_time=0
                if scenario in ['context','context_paste']:
                    os.write(writer,b'x')
                elif scenario in ['multiline','multiline_large']:
                    assert child.poll() is None,scenario+': pasted newline submitted prematurely'
                    suffix=b'synthetic-tail' if scenario=='multiline' else b'synthetic-tail'*4096
                    pending=suffix+b'\x1b[201~\r'
        while select.select([master],[],[],.03)[0]:
            raw.extend(os.read(master,65536))
        assert child.returncode==0,(scenario,bytes(raw))
        assert b'ADMIN_KEY_TEST_DONE' in raw,(scenario,bytes(raw))
        assert b'synthetic' not in raw,(scenario,bytes(raw))
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
        if child.poll() is None:
            child.kill()
            child.wait()
        if inherited!=control:
            os.close(inherited)
        for fd in [control,writer,master,slave]:
            os.close(fd)
`
	command := exec.Command(python, "-I", "-B", "-c", script, os.Args[0])
	output, err := command.CombinedOutput()
	require.NoError(t, err, "%s", output)
	t.Logf("%s", output)
}
