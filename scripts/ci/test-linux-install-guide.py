"""Exercise both documented Linux installs with real shell, curl and checksum tools."""

import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
from itertools import product
import os
from pathlib import Path
import re
import ssl
import subprocess
import tempfile
import threading


repo = Path(__file__).resolve().parents[2]
launcher = (repo / "scripts/openai.sh").read_bytes()
manifest = (repo / "scripts/openai.sha256").read_bytes()
if manifest != f"{hashlib.sha256(launcher).hexdigest()}  openai.sh\n".encode():
    raise SystemExit("The Linux launcher does not match its reviewed checksum.")


def section_command(path, heading):
    documented = (repo / path).read_text().split(f"\n### {heading}\n", 1)[1]
    section = re.split(r"^#{1,3} ", documented, maxsplit=1, flags=re.MULTILINE)[0]
    [command] = re.findall(r"^```sh\n(.*?)\n```$", section, re.MULTILINE | re.DOTALL)
    return section, command


linux, paste = section_command("README.md", "Linux")
_, ssh_paste = section_command("docs/installation.md", "Install over SSH")
if not re.search(r"\]\(https://github\.com/openai/openai-cli/releases/download/v[0-9]+\.[0-9]+\.[0-9]+/openai\.sh\)", linux):
    raise SystemExit("The Linux download must link to a fixed, published release asset.")
# The README can pin an earlier published launcher when the checkout advances.
published_digest = "3b94b2ac8e73bd16f3803ceea1ba5c08b1c80a3c9b27f75c5b8a9d4d76ff7e00"
if f"{published_digest}  openai.sh" not in paste:
    raise SystemExit("The Linux README command must check the reviewed launcher checksum.")
# Verify new pins against the published installer before updating these expectations.
revision = "334f332fc6a5f8298b20548d4fbbbb30c940cdc5"
pin = "b0c05af08bd5e168366063bde36147d880f90fba337ce57fff928aa5f3633ce5"
origin = "https://raw.githubusercontent.com"
url = f"{origin}/openai/openai-cli/{revision}/scripts/install-linux-release.sh"
installer = b'echo installer >> "$CALLS"\nexit "$INSTALL_RC"\n'

# Run the paste verbatim. Map its pinned checksum to our synthetic installer.
# The system checksum tool still verifies the exact bytes returned by curl.
fixtures = r'''
curl() {
  local previous= protocol= redirect= urls=
  for arg do
    case "$previous" in --proto) protocol=$arg ;; --proto-redir) redirect=$arg ;; esac
    case "$arg" in *://*) urls="$urls $arg" ;; esac
    previous=$arg
  done
  [ "$protocol" = '=https' ] && [ "$redirect" = '=https' ] && [ "$urls" = " $GUIDE_URL" ] || return 90
  command curl "$@" --noproxy '*' --connect-to "::127.0.0.1:$PORT" --cacert "$CERT"
}
sha256sum() {
  if [ "$1" = -c ]; then command sha256sum "$@"; return; fi
  read -r digest file && [ "$digest" = "$GUIDE_DIGEST" ] || return 1
  printf '%s  %s\n' "$FIXTURE_DIGEST" "$file" | command sha256sum "$@"
}
source() {
  if [ "$1" = ./openai.sh ]; then builtin source "$@"; return; fi
  [ "$#" = 1 ] && [ "$1" = /usr/share/openai-cli/enable-updates.sh ] || return 1
  echo helper >> "$CALLS"
  [ "$HELPER_RC" = 0 ] || return "$HELPER_RC"
  setup_applied=1
}
'''
check_shell = '''
setup_rc=$?
if [ "$setup_rc" = 0 ]; then [ "${setup_applied:-}" = 1 ] || exit 91
else [ -z "${setup_applied:-}" ] || exit 92
fi
exit "$setup_rc"
'''


class Download(BaseHTTPRequestHandler):
    def do_GET(self):
        self.server.requests += 1
        if self.path != url.removeprefix(origin) or not self.server.responses:
            self.send_error(404)
            return
        status, body = self.server.responses.pop(0)
        self.send_response(status)
        length = len(body) + (1 if self.server.truncated else 0)
        self.send_header("Content-Length", str(length))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


with tempfile.TemporaryDirectory() as tmp, HTTPServer(("127.0.0.1", 0), Download) as server:
    cert, key, calls = (Path(tmp) / name for name in ("cert", "key", "calls"))
    (Path(tmp) / "openai.sh").write_bytes(launcher)
    subprocess.run(["openssl", "req", "-newkey", "rsa:2048", "-nodes", "-x509", "-days", "1",
                    "-subj", "/CN=raw.githubusercontent.com", "-addext", "subjectAltName=DNS:raw.githubusercontent.com",
                    "-keyout", key, "-out", cert], check=True, capture_output=True)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.minimum_version = ssl.TLSVersion.TLSv1_2
    tls.load_cert_chain(cert, key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        shells = {"bash": ["--noprofile", "--norc"], "zsh": ["-f"]}
        for shell, flags in shells.items():
            for args in (["-c", "builtin source ./openai.sh"], ["-i", "./openai.sh"]):
                result = subprocess.run([shell, *flags, *args], cwd=tmp,
                                        env={"PATH": "/usr/bin:/bin", "HOME": tmp, "HTTPS_PROXY": "http://127.0.0.1:1"},
                                        stdin=subprocess.DEVNULL, capture_output=True, timeout=5)
                if result.returncode != 1 or b"Run source ./openai.sh" not in result.stderr:
                    raise SystemExit(f"{shell} accepted an executed or noninteractive launcher")
            # A leftover file can share the expected name after a browser renames a new download.
            stale = b'echo stale >> "$CALLS"\n'
            for name, files in {
                "changed download": {"openai.sh": stale},
                "partial download": {"openai.sh": launcher[:len(launcher) // 2]},
                "wrong filename": {"openai (1).sh": launcher},
                "stale copy": {"openai.sh": stale, "openai (1).sh": launcher},
            }.items():
                with tempfile.TemporaryDirectory(dir=tmp) as folder:
                    for filename, data in files.items():
                        (Path(folder) / filename).write_bytes(data)
                    calls.write_text("")
                    trace = 'source() { echo source >> "$CALLS"; builtin source "$@"; }\n'
                    env = {"PATH": os.environ["PATH"], "HOME": tmp, "CALLS": str(calls), "HTTPS_PROXY": "http://127.0.0.1:1"}
                    result = subprocess.run([shell, *flags, "-ic", trace + paste], cwd=folder, env=env,
                                            stdin=subprocess.DEVNULL, capture_output=True, timeout=5)
                    if result.returncode != 1 or calls.read_text():
                        raise SystemExit(f"{shell} README: {name} passed verification or ran a file")
                    print(f"PASS {shell} README: {name}")
        # Bind the download fixture to its actual bytes. For the initial release these pins match.
        readme_fixture = paste.replace(published_digest, hashlib.sha256(launcher).hexdigest())
        routes = {"README": readme_fixture, "SSH guide": ssh_paste}
        for (shell, flags), (route, entrypoint) in product(shells.items(), routes.items()):
            for name, responses, installer_rc, helper_rc, expected_codes, expected_calls in (
                ("retry", [(503, b"busy"), (200, installer)], 0, 0, (0,), ["installer", "helper"]),
                ("empty", [(200, b"")], 0, 0, (1,), []),
                # Curl can report a partial transfer or a TLS receive error on premature EOF.
                ("truncated", [(200, installer)], 0, 0, (18, 56), []),
                ("altered", [(200, b'echo altered >> "$CALLS"\n')], 0, 0, (1,), []),
                ("missing", [(404, installer)], 0, 0, (22,), []),
                ("failed installer", [(200, installer)], 37, 0, (37,), ["installer"]),
                ("failed helper", [(200, installer)], 0, 43, (43,), ["installer", "helper"]),
            ):
                server.responses = list(responses)
                server.truncated = name == "truncated"
                server.requests = 0
                calls.write_text("")
                env = dict.fromkeys(("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"), "http://127.0.0.1:1")
                env.update(HOME=tmp, PATH=os.environ["PATH"], CERT=str(cert), PORT=str(server.server_port),
                           GUIDE_URL=url, GUIDE_DIGEST=pin, FIXTURE_DIGEST=hashlib.sha256(installer).hexdigest(),
                           CALLS=str(calls), INSTALL_RC=str(installer_rc), HELPER_RC=str(helper_rc))
                try:
                    result = subprocess.run([shell, *flags, "-ic", fixtures + entrypoint + check_shell],
                                            cwd=tmp, env=env, stdin=subprocess.DEVNULL, capture_output=True, timeout=15)
                except subprocess.TimeoutExpired:
                    raise SystemExit(f"{shell} {route}: {name} timed out") from None
                observed = calls.read_text().splitlines()
                if server.requests != len(responses) or result.returncode not in expected_codes or observed != expected_calls:
                    raise SystemExit(f"{shell} {route}: {name} failed: status {result.returncode}, "
                                     f"{server.requests} requests, calls {observed!r}, stderr {result.stderr!r}")
                print(f"PASS {shell} {route}: {name}")
    finally:
        server.shutdown()
