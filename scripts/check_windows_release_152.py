"""Check the published Windows x64 CLI on an ephemeral GitHub Actions Windows runner."""

import argparse
from contextlib import contextmanager
from hashlib import sha256
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import platform
import re
import secrets
import subprocess
import sys
import tempfile
import threading
from urllib.parse import parse_qsl, urlsplit


RELEASE = 'v1.52.0'
EXE_SHA = '79218dbc28c010d335101f13e96f103f7f7174808d08c575925f8535a151361e'
SUFFIX = ' --format text admin organization projects list'
TEXT = 'one two three four'
IDS = [606, 1403, 2380, 3116]
OFFSETS = [0, 3, 7, 13, 18]
PROJECT_ID = 'proj_windows_release_fixture'
PROJECT_NAME = 'Synthetic release project'
SHELLS = ('powershell51', 'powershell7')


class CheckFailure(Exception):
    """A safe diagnostic with no subprocess output or environment data."""


def require(value, message):
    if not value:
        raise CheckFailure(message)


def digest(data):
    return sha256(data).hexdigest()


def file_digest(path):
    with path.open('rb') as file:
        return sha256(file.read()).hexdigest()


def make_environment(profile, windows, cmd, endpoint):
    for part in ('roaming', 'local', 'temp', 'config'):
        (profile / part).mkdir(parents=True)
    # Unexpected external HTTP destinations reach the non-forwarding loopback fixture.
    return dict(SystemRoot=str(windows), WINDIR=str(windows), COMSPEC=str(cmd),
                PATH=str(cmd.parent), PATHEXT='.COM;.EXE;.BAT;.CMD',
                USERPROFILE=str(profile), HOME=str(profile), APPDATA=str(profile / 'roaming'),
                LOCALAPPDATA=str(profile / 'local'), XDG_CONFIG_HOME=str(profile / 'config'),
                TEMP=str(profile / 'temp'), TMP=str(profile / 'temp'),
                POWERSHELL_TELEMETRY_OPTOUT='1', POWERSHELL_UPDATECHECK='Off',
                DOTNET_CLI_TELEMETRY_OPTOUT='1', NO_COLOR='1',
                OPENAI_BASE_URL=endpoint + '/v1', NO_PROXY='127.0.0.1',
                HTTP_PROXY=endpoint, HTTPS_PROXY=endpoint)


def execute(args, env, folder, facts, key):
    process = subprocess.Popen(args, cwd=folder, env=env, stdin=subprocess.DEVNULL,
                               stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    try:
        stdout, stderr = process.communicate(timeout=35)
    except subprocess.TimeoutExpired:
        killer = Path(env['SystemRoot']) / 'System32' / 'taskkill.exe'
        try:
            if process.poll() is None:
                subprocess.run([str(killer), '/PID', str(process.pid), '/T', '/F'], env=env,
                               stdin=subprocess.DEVNULL, capture_output=True, timeout=10)
        except (OSError, subprocess.TimeoutExpired):
            pass
        finally:
            if process.poll() is None:
                process.kill()
            process.wait(timeout=10)
            process.stdout.close()
            process.stderr.close()
        raise CheckFailure('process timeout')
    facts.update(exit=process.returncode, stdout_bytes=len(stdout), stderr_bytes=len(stderr),
                 stdout_sha256=digest(stdout), stderr_sha256=digest(stderr))
    leaks = [key.encode(encoding) in stream for encoding in ('utf-8', 'utf-16le', 'utf-16be')
             for stream in (stdout, stderr)]
    facts['credential_in_streams'] = any(leaks)
    require(not any(leaks), 'synthetic key found in process output')
    return stdout


@contextmanager
def fixture(key):
    records = []
    project = dict(id=PROJECT_ID, object='organization.project', name=PROJECT_NAME,
                   created_at=1, status='active')
    body = json.dumps(dict(object='list', data=[project], has_more=False)).encode()

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *args):
            pass

        def respond(self):
            url = urlsplit(self.path)
            expected_path = '/v1/organization/projects'
            matches = not url.netloc and url.path == expected_path
            auth = self.headers.get('Authorization') == 'Bearer ' + key
            # Keep actual safe pagination fields separately. Never export a secret query value.
            query = []
            for name, value in parse_qsl(url.query, keep_blank_values=True):
                safe = name == 'limit' and re.fullmatch(r'[0-9]{1,8}', value)
                query.append([name, value] if safe else ['<other>', '<redacted>'])
            record = dict(method=self.command, path=expected_path if matches else '<unexpected>',
                          query=query[:8], authorized=auth)
            records.append(record)
            ok = self.command == 'GET' and matches and auth
            payload = body if ok else b'{}'
            self.send_response(200 if ok else 404)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        do_GET = do_POST = do_PUT = do_DELETE = do_PATCH = do_HEAD = do_CONNECT = respond

    with ThreadingHTTPServer(('127.0.0.1', 0), Handler) as server:
        port = server.server_address[1]
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            yield f'http://127.0.0.1:{port}', records
        finally:
            server.shutdown()
            thread.join()


def get_command(stdout, exe):
    guide = stdout.decode('utf-8').replace('\r\n', '\n')
    before, step7, remainder = guide.partition('\n7. TEST THE KEY\n')
    section, step8, _ = remainder.partition('\n8. RETRY YOUR ORIGINAL ADMIN COMMAND\n')
    require(before and step7 and step8, 'missing admin guide step')
    candidates = [line.lstrip(' \t') for line in section.splitlines() if line.endswith(SUFFIX)]
    require(len(candidates) == 1, 'expected one step 7 verification command')
    command = candidates[0]
    invocation = command[:-len(SUFFIX)]
    quoted = "'" + str(exe).replace("'", "''") + "'"
    # Allow an unquoted absolute path only if it is a single safe shell token.
    permitted = ['& ' + quoted]
    if re.fullmatch(r'[a-zA-Z0-9:/\\._-]+', str(exe)):
        permitted.append('& ' + str(exe))
    require(invocation in permitted, 'guide command did not identify the pinned executable')
    return command


def check_tokenizer(stdout):
    obj = json.loads(stdout)
    tokens = obj['tokens']
    require((obj['encoding'], obj['input_bytes'], obj['token_count'], len(tokens)) ==
            ('cl100k_base', 18, 4, 4), 'unexpected encoding or counts')
    actual = [(t['id'], t['start_byte'], t['end_byte'], t['bytes_hex'], t['text']) for t in tokens]
    expected = [(token, start, end, TEXT[start:end].encode().hex(), TEXT[start:end])
                for token, start, end in zip(IDS, OFFSETS, OFFSETS[1:])]
    require(actual == expected, 'token IDs, offsets, bytes, or text differ')
    require(b''.join(bytes.fromhex(t['bytes_hex']) for t in tokens) == TEXT.encode(),
            'token bytes did not reconstruct the input')
    return dict(ids=IDS, byte_offsets=OFFSETS, bytes_hex=[t['bytes_hex'] for t in tokens],
                reconstructed_bytes=len(TEXT.encode()))


def project_fields(stdout):
    lines = [line.strip() for line in stdout.splitlines()]
    return dict(project_id_match=lines.count(f'ID: {PROJECT_ID}'.encode()) == 1,
                project_name_match=lines.count(f'Name: {PROJECT_NAME}'.encode()) == 1)


def ps_file(shell, command, expected_status, env, profile, facts, key):
    script = profile / 'check.ps1'
    # Windows PowerShell 5.1 needs a BOM for UTF-8 file input.
    script.write_text(command + """
$saved = $LASTEXITCODE
if ($null -eq $saved) { exit 126 }
[Console]::Out.WriteLine("CHECK_SAVED_EXIT=$saved")
[Console]::Out.WriteLine("CHECK_SHELL_CONTINUED")
exit $saved
""", encoding='utf-8-sig')
    stdout = execute([str(shell), '-NoLogo', '-NoProfile', '-NonInteractive', '-File', str(script)],
                     env, profile, facts, key)
    lines = stdout.splitlines()
    marker = f'CHECK_SAVED_EXIT={expected_status}'.encode()
    facts['saved_status_match'] = lines.count(marker) == 1
    facts['continuation_match'] = lines.count(b'CHECK_SHELL_CONTINUED') == 1
    require(facts['exit'] == expected_status and facts['stderr_bytes'] == 0 and
            facts['saved_status_match'] and facts['continuation_match'], 'native exit or continuation mismatch')
    return stdout


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('executable', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    output = args.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    require(not any(output.iterdir()), 'receipt directory must be empty')
    names = ['guide', *SHELLS, 'tokenizer', 'cmd_status', *(s+'_status' for s in SHELLS)]
    checks = {name: {'result': 'NOT_RUN'} for name in names}
    receipt = dict(release=RELEASE, platform=platform.platform(), machine=platform.machine(), checks=checks,
                   interrupted_powershell='NOT_RUN', physical_terminal='NOT_RUN')
    key = 'synthetic-admin-' + secrets.token_hex(16)

    def save():
        tmp = output / 'receipt.tmp'
        tmp.write_text(json.dumps(receipt, indent=2) + '\n', encoding='utf-8')
        tmp.replace(output / 'receipt.json')

    def test(name, action):
        try:
            result = action(checks[name])
            checks[name]['result'] = 'PASS'
            return result
        except Exception as exc:
            checks[name].update(result='FAIL', reason=str(exc) if isinstance(exc, CheckFailure) else type(exc).__name__)
        finally:
            save()

    save()
    try:
        require(os.name == 'nt' and os.environ.get('GITHUB_ACTIONS') == 'true'
                and os.environ.get('RUNNER_ARCH') == 'X64' and os.environ.get('RUNNER_OS') == 'Windows',
                'this checker requires the designated GitHub Actions Windows x64 job')
        exe = args.executable.resolve(strict=True)
        require(exe.name.lower() == 'openai.exe' and not any(v in str(exe) for v in ('%', '"', '\r', '\n')),
                'unsupported release executable path')
        receipt.update(executable_sha256=file_digest(exe), path_has_spaces=' ' in str(exe),
                       path_has_apostrophe="'" in str(exe))
        require(receipt['executable_sha256'] == EXE_SHA, 'pinned executable hash mismatch')
        windows = Path(os.environ['SystemRoot'])
        cmd = windows / 'System32' / 'cmd.exe'
        shells = {'powershell51': cmd.parent / 'WindowsPowerShell/v1.0/powershell.exe',
                  'powershell7': Path(os.environ['ProgramFiles']) / 'PowerShell/7/pwsh.exe'}
        receipt['cmd'] = dict(path=str(cmd), sha256=file_digest(cmd), version=platform.version())
        with tempfile.TemporaryDirectory() as temp, fixture(key) as (endpoint, requests):
            parent = Path(temp)
            def isolated(name):
                profile = parent / name
                return profile, make_environment(profile, windows, cmd, endpoint)

            def guide(facts):
                profile, env = isolated('guide')
                command = f'"{cmd}" /d /v:off /s /c ""{exe}" help --all setup admin"'
                stdout = execute(command, env, profile, facts, key)
                require(facts['exit'] == 0 and facts['stderr_bytes'] == 0, 'cmd could not emit the guide')
                (output / 'cmd-guide.txt').write_bytes(stdout)
                line = get_command(stdout, exe)
                facts['line_sha256'] = digest(line.encode())
                return line

            line = test('guide', guide)
            if line:
                for name, shell in shells.items():
                    def consume(facts):
                        profile, env = isolated(name)
                        facts.update(path=str(shell), shell_sha256=file_digest(shell), line_sha256=digest(line.encode()))
                        version = {}
                        raw = execute([str(shell), '-NoLogo', '-NoProfile', '-NonInteractive', '-Command',
                                       '$PSVersionTable.PSVersion.ToString()'], env, profile, version, key)
                        number = raw.decode('ascii').strip()
                        require(version['exit'] == 0 and not version['stderr_bytes'] and
                                re.fullmatch(r'[0-9]+(?:\.[0-9]+){1,3}', number) and
                                number.startswith('5.1.' if name == 'powershell51' else '7.'), 'unexpected shell version')
                        facts['version'] = number
                        env['OPENAI_ADMIN_KEY'] = key
                        start = len(requests)
                        try:
                            stdout = ps_file(shell, line, 0, env, profile, facts, key)
                        finally:
                            facts['requests'] = requests[start:start + 8]
                            facts['request_count'] = len(requests) - start
                        require(facts['request_count'] == 1 and facts['requests'][0]['method'] == 'GET' and
                                facts['requests'][0]['path'] == '/v1/organization/projects' and
                                facts['requests'][0]['authorized'], 'expected exactly one authenticated project list')
                        facts.update(project_fields(stdout))
                        require(facts['project_id_match'] and facts['project_name_match'], 'fixture project missing in CLI output')
                    test(name, consume)

            def tokenizer(facts):
                profile, env = isolated('tokenizer')
                stdout = execute([str(exe), '--format', 'json', 'tokenizer', 'inspect', '--encoding',
                                  'cl100k_base', '--text', TEXT], env, profile, facts, key)
                require(facts['exit'] == 0 and facts['stderr_bytes'] == 0, 'tokenizer process failed')
                facts.update(check_tokenizer(stdout))
            # A successful cmd guide demonstrates that this host permits the pinned executable.
            if checks['guide'].get('exit') == 0:
                test('tokenizer', tokenizer)

            def cmd_status(facts):
                profile, env = isolated('cmd_status')
                for code in (130, 1):
                    observed = facts[str(code)] = {}
                    execute([str(cmd), '/d', '/v:off', '/c', f'exit {code}'], env, profile, observed, key)
                    require(observed['exit'] == code and observed['stderr_bytes'] == 0, 'direct cmd status mismatch')
            test('cmd_status', cmd_status)
            for name, shell in shells.items():
                def ps_status(facts):
                    profile, env = isolated(name + '_status')
                    for code in (130, 1):
                        observed = facts[str(code)] = {}
                        ps_file(shell, f"& '{cmd}' /d /v:off /c 'exit {code}'", code, env, profile, observed, key)
                test(name + '_status', ps_status)
            receipt['total_requests'] = len(requests)
            expected_requests = sum(checks[name].get('request_count', 0) for name in SHELLS)
            require(len(requests) == expected_requests, 'observed requests outside the two guide consumers')
        receipt['cleanup'] = 'fixture stopped and temporary profiles removed'
    except Exception as exc:
        receipt['error'] = str(exc) if isinstance(exc, CheckFailure) else type(exc).__name__
    receipt['result'] = 'PASS' if 'error' not in receipt and all(v['result'] == 'PASS' for v in checks.values()) else 'INCOMPLETE'
    save()
    print(json.dumps({'result':receipt['result'], 'checks':{k:v['result'] for k,v in checks.items()}}))
    return 0 if receipt['result'] == 'PASS' else 1


if __name__ == '__main__':
    sys.exit(main())
