#!/usr/bin/env python3
"""Serve fixed synthetic list pages on loopback for checks and recordings."""
import argparse
import contextlib
import http.server
import json
from pathlib import Path
import select
import signal
import socket
import threading
import urllib.parse


def identifier(page, row=1, length=0, prefix='file-demo-'):
    suffix = f'-{page:03d}-{row:03d}'
    return prefix + ('x' * max(0, length - len(prefix) - len(suffix))) + suffix


def file_identifier(server, page, row=1):
    if server.numbered_files and server.resource == 'files':
        return f'file-demo-{(page - 1) * server.rows + row:03d}'
    return identifier(page, row, server.id_length)


class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def do_GET(self):
        server = self.server
        parsed = urllib.parse.urlsplit(self.path)
        query = urllib.parse.parse_qs(parsed.query)
        cursor = query.get('after', [''])[0]
        page = 1
        if cursor:
            candidates = [index for index in range(1, server.pages + 1)
                          if cursor == file_identifier(server, index, server.rows)]
            page = candidates[0] + 1 if candidates else 0
        with server.lock:
            server.requests.append({'page': page, 'cursor': cursor, 'path': parsed.path})
            server.inflight += 1
            server.maximum_inflight = max(server.maximum_inflight, server.inflight)
        try:
            route = 'organization/projects' if server.resource == 'projects' else server.resource
            if parsed.path != '/v1/' + route or not 1 <= page <= server.pages:
                status, value = 400, {'error': {'message': 'Synthetic invalid cursor or route.', 'type': 'invalid_request_error'}}
            else:
                if page == server.block_page:
                    server.blocked.set()
                    while not server.release.wait(0.02):
                        if select.select([self.connection], [], [], 0)[0]:
                            if not self.connection.recv(1, socket.MSG_PEEK):
                                server.disconnected.set()
                                return
                if page == server.error_page:
                    status, value = 400, {'error': {'message': 'Synthetic page failure.', 'type': 'invalid_request_error'}}
                else:
                    items = [{'id': file_identifier(server, page, row), 'object': 'file',
                              'filename': (f'dataset-{(page - 1) * server.rows + row:03d}.jsonl'
                                           if server.numbered_files else f'page-{page:03d}-item-{row:03d}.jsonl'),
                              'purpose': 'batch',
                              'bytes': row * 18432, 'status': 'processed', 'created_at': 1704067200}
                             for row in range(1, server.rows + 1)]
                    if server.resource == 'models':
                        items = [{'id': identifier(1, row, prefix='model-demo-'), 'object': 'model',
                                  'owned_by': 'openai', 'created': 1704067200}
                                 for row in range(1, server.rows + 1)]
                    elif server.resource == 'batches':
                        items = [{'id': identifier(page, row), 'object': 'batch', 'status': 'completed',
                                  'endpoint': '/v1/responses', 'created_at': 1704067200,
                                  'request_counts': {'total': 1, 'completed': 1, 'failed': 0}}
                                 for row in range(1, server.rows + 1)]
                    elif server.resource == 'projects':
                        items = [{'id': identifier(page, row), 'object': 'organization.project',
                                  'name': f'Synthetic project {row}', 'created_at': 1704067200, 'status': 'active'}
                                 for row in range(1, server.rows + 1)]
                    status, value = 200, {'object': 'list', 'data': items, 'has_more': page < server.pages,
                                          'first_id': items[0]['id'] if items else '', 'last_id': items[-1]['id'] if items else ''}
            body = json.dumps(value).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            server.disconnected.set()
        except Exception as error:
            with server.lock:
                server.errors.append(str(error))
        finally:
            with server.lock:
                server.inflight -= 1
                if server.log_path:
                    try:
                        with server.log_path.open('a', encoding='utf-8') as log:
                            log.write(json.dumps({'page': page, 'status': locals().get('status'),
                                                  'cursor_valid': 1 <= page <= server.pages}) + '\n')
                    except OSError as error:
                        server.errors.append(str(error))


@contextlib.contextmanager
def serve(*, pages=3, rows=1, id_length=0, block_page=0, error_page=0, log_path=None,
          resource='files', numbered_files=False):
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    server.pages, server.rows, server.id_length = pages, rows, id_length
    server.resource = resource
    server.numbered_files = numbered_files
    server.block_page, server.error_page, server.log_path = block_page, error_page, log_path
    server.lock = threading.Lock()
    server.requests, server.errors = [], []
    server.inflight = server.maximum_inflight = 0
    server.blocked, server.release, server.disconnected = threading.Event(), threading.Event(), threading.Event()
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.release.set()
        server.shutdown()
        server.server_close()
        thread.join(timeout=3)
        assert not thread.is_alive(), 'synthetic server did not stop'
        assert not server.errors, server.errors


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('address', type=Path)
    parser.add_argument('request_log', type=Path)
    parser.add_argument('--pages', type=int, default=2)
    parser.add_argument('--rows', type=int, default=1)
    parser.add_argument('--numbered-files', action='store_true',
                        help='Number file IDs and filenames across all pages.')
    args = parser.parse_args()
    if args.pages < 1 or args.rows < 0:
        parser.error('pages must be positive and rows must be nonnegative')
    address, log = args.address, args.request_log
    log.touch(exist_ok=False)
    stopped = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stopped.set())
    signal.signal(signal.SIGINT, lambda *_: stopped.set())
    with serve(pages=args.pages, rows=args.rows, log_path=log, numbered_files=args.numbered_files) as server:
        address.write_text(f'http://127.0.0.1:{server.server_port}/v1')
        stopped.wait()


if __name__ == '__main__':
    main()
