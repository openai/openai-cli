"""Run the Linux install paste with real curl retries and harmless installer bytes."""

import hashlib
from http.server import BaseHTTPRequestHandler, HTTPServer
import os
from pathlib import Path
import ssl
import subprocess
import tempfile
import threading


guide = (Path(__file__).resolve().parents[2] / "docs/installation.md").read_text()
linux = guide.split("\n## Linux\n", 1)[1].split("\n## ", 1)[0]
paste = linux.split("```sh\n", 1)[1].split("\n```", 1)[0]
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
  read -r digest file && [ "$digest" = "$GUIDE_DIGEST" ] || return 1
  printf '%s  %s\n' "$FIXTURE_DIGEST" "$file" | command sha256sum "$@"
}
source() {
  [ "$#" = 1 ] && [ "$1" = /usr/share/openai-cli/enable-updates.sh ] || return 1
  echo helper >> "$CALLS"
}
'''


class Download(BaseHTTPRequestHandler):
    def do_GET(self):
        self.server.requests += 1
        if self.path != url.removeprefix(origin) or not self.server.responses:
            self.send_error(404)
            return
        status, body = self.server.responses.pop(0)
        self.send_response(status)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        pass


with tempfile.TemporaryDirectory() as tmp, HTTPServer(("127.0.0.1", 0), Download) as server:
    cert, key, calls = (Path(tmp) / name for name in ("cert", "key", "calls"))
    subprocess.run(["openssl", "req", "-newkey", "rsa:2048", "-nodes", "-x509", "-days", "1",
                    "-subj", "/CN=raw.githubusercontent.com", "-addext", "subjectAltName=DNS:raw.githubusercontent.com",
                    "-keyout", key, "-out", cert], check=True, capture_output=True)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(cert, key)
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        for shell in ("bash", "zsh"):
            for name, responses, installer_rc, expected_rc, expected_calls in (
                ("retry", [(503, b"busy"), (200, installer)], 0, 0, ["installer", "helper"]),
                ("empty", [(200, b"")], 0, 1, []),
                ("altered", [(200, b'echo altered >> "$CALLS"\n')], 0, 1, []),
                ("missing", [(404, installer)], 0, 22, []),
                ("failed installer", [(200, installer)], 37, 37, ["installer"]),
            ):
                server.responses = list(responses)
                server.requests = 0
                calls.write_text("")
                env = dict.fromkeys(("HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"), "http://127.0.0.1:1")
                env.update(HOME=tmp, PATH=os.environ["PATH"], CERT=str(cert), PORT=str(server.server_port),
                           GUIDE_URL=url, GUIDE_DIGEST=pin, FIXTURE_DIGEST=hashlib.sha256(installer).hexdigest(),
                           CALLS=str(calls), INSTALL_RC=str(installer_rc))
                try:
                    result = subprocess.run([shell, "-c", fixtures + paste], env=env, capture_output=True, timeout=15)
                except subprocess.TimeoutExpired:
                    raise SystemExit(f"{shell} {name} timed out") from None
                observed = calls.read_text().splitlines()
                if server.requests != len(responses) or result.returncode != expected_rc or observed != expected_calls:
                    raise SystemExit(f"{shell} {name} failed: status {result.returncode}, "
                                     f"{server.requests} requests, calls {observed!r}, stderr {result.stderr!r}")
                print(f"PASS {shell} install guide: {name}")
    finally:
        server.shutdown()
