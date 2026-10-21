"""Exercise the CLI logging wrapper through local HTTP/HTTPS servers.

Run with python3 -B -m unittest discover -s evals/scripts -p 'test_bk_request_log.py'.
Requires Node.js 22.21 or 24.5+ and openssl; no Buildkite credentials or external API calls.
"""

from contextlib import contextmanager
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import select
import shutil
import socket
import ssl
import subprocess
import tempfile
import threading
import unittest


SCRIPTS = Path(__file__).resolve().parent


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
        self.server.received.append((self.path, self.headers, body))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b'upstream response')

    def do_CONNECT(self):
        self.server.received.append(self.path)
        with socket.create_connection(('127.0.0.1', self.server.target_port)) as upstream:
            self.send_response(200)
            self.end_headers()
            sockets = [self.connection, upstream]
            while True:
                ready, _, _ = select.select(sockets, [], [], 5)
                if not ready:
                    return
                for source in ready:
                    data = source.recv(65536)
                    if not data:
                        return
                    (upstream if source is self.connection else self.connection).sendall(data)


@contextmanager
def server(tls=None, target_port=None, host='127.0.0.1'):
    srv = ThreadingHTTPServer((host, 0), Handler)
    srv.received = []
    srv.target_port = target_port
    if tls:
        srv.socket = tls.wrap_socket(srv.socket, server_side=True)
    thread = threading.Thread(target=srv.serve_forever, daemon=True)
    thread.start()
    try:
        yield srv
    finally:
        srv.shutdown()
        srv.server_close()
        thread.join()


class RequestLogTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.node = shutil.which('node')
        self.bk = self.root / 'bk'
        # Only the CLI process is a fixture. The shim, its upstream transport,
        # persisted log and rendered struggle report are production code.
        self.bk.write_text(f'#!{self.node}\n' + '''
const http = require('node:http');
const base = process.env.TEST_API === 'graphql'
  ? process.env.BUILDKITE_GRAPHQL_ENDPOINT : process.env.BUILDKITE_REST_API_ENDPOINT;
const target = new URL(base.replace(/[/]$/, '') + process.env.TEST_PATH);
const req = http.request(target, {method: 'POST', headers: {
  authorization: 'Bearer fixture-header-secret', 'content-type': 'application/json'
}}, res => {
  res.pipe(process.stdout);
  res.on('end', () => process.exit(res.statusCode === 200 ? Number(process.env.TEST_EXIT || 0) : 8));
});
req.on('error', () => process.exit(9));
req.end(JSON.stringify({payload: 'fixture-body-secret', args: process.argv.slice(2)}));
''')
        self.bk.chmod(0o755)
        self.env = {k: v for k, v in os.environ.items()
                    if k.lower() not in ('http_proxy', 'https_proxy', 'no_proxy', 'all_proxy')
                    and k not in ('NODE_USE_ENV_PROXY', 'NODE_OPTIONS')}
        self.log = self.root / 'requests.jsonl'
        self.env.update(BK_SHIM_REAL_BK=str(self.bk), BK_SHIM_REQUEST_LOG=str(self.log),
                        TEST_PATH='/v2/builds?access_token=fixture-query-secret', TEST_API='rest')

    def run_bk(self, endpoint, args, **env):
        self.log.unlink(missing_ok=True)
        return subprocess.run([self.node, str(SCRIPTS / 'bk-request-log-shim.mjs'), *args],
                              env={**self.env, 'BUILDKITE_REST_API_ENDPOINT': endpoint,
                                   'BUILDKITE_GRAPHQL_ENDPOINT': endpoint, **env},
                              text=True, capture_output=True, timeout=15)

    def test_credentials_are_not_persisted_or_rendered(self):
        args = ['--quiet', 'api', '/v2/builds?access_token=fixture-argument-secret',
                '--header', 'Authorization: Bearer fixture-header-secret',
                '--data={"password":"fixture-body-secret"}']
        with server() as upstream:
            for args, command, exit_code in ((args, 'bk api', 0), (args, 'bk api', 7),
                                              (['--quiet', 'build', 'view', 'fixture-argument-secret'], 'bk build view', 0),
                                              (['fixture-argument-secret'], 'bk', 7)):
                with self.subTest(command=command, exit_code=exit_code):
                    result = self.run_bk(f'http://127.0.0.1:{upstream.server_port}/', args,
                                         TEST_EXIT=str(exit_code))
                    self.assertEqual(result.returncode, exit_code, result.stderr)
                    self.assertEqual(result.stdout, 'upstream response')
                    path, headers, body = upstream.received[-1]
                    self.assertIn('access_token=fixture-query-secret', path)
                    self.assertEqual(headers['Authorization'], 'Bearer fixture-header-secret')
                    self.assertEqual(json.loads(body)['args'], args)
                    records = [json.loads(line) for line in self.log.read_text().splitlines()]
                    self.assertEqual(len(records), 2)
                    self.assertEqual(records[0]['url'], f'http://127.0.0.1:{upstream.server_port}/v2/builds')
                    self.assertEqual(records[1]['exit_code'], exit_code)
                    summary = subprocess.check_output(
                        ['bash', str(SCRIPTS / 'api-fanout.sh'), 'json', str(self.log)], text=True)
                    self.assertEqual(json.loads(summary)['bk_cli']['commands'][0]['command'], command)
                    report = self.root / 'summary.json'
                    report.write_text(summary)
                    rendered = subprocess.check_output(
                        ['bash', str(SCRIPTS / 'api-fanout.sh'), 'errors-markdown', str(report)], text=True)
                    for value in ('fixture-query-secret', 'fixture-argument-secret',
                                  'fixture-header-secret', 'fixture-body-secret'):
                        self.assertNotIn(value, self.log.read_text() + summary + rendered)

    def test_outbound_proxy_and_no_proxy(self):
        cert, key = self.root / 'cert.pem', self.root / 'key.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-subj', '/CN=api.invalid', '-addext', 'subjectAltName=DNS:api.invalid',
                        '-keyout', str(key), '-out', str(cert)],
                       check=True, capture_output=True)
        tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls.load_cert_chain(cert, key)
        for protocol in ('http', 'https'):
            with self.subTest(protocol=protocol), server(tls if protocol == 'https' else None) as upstream, \
                    server(target_port=upstream.server_port) as proxy:
                proxy_url = f'http://127.0.0.1:{proxy.server_port}'
                # api.invalid cannot resolve directly. The CONNECT proxy alone
                # routes it to our upstream; HTTPS additionally checks TLS/SNI.
                for index, casing in enumerate(('upper', 'lower', 'both'), start=1):
                    env = {f'{protocol}_proxy'.upper() if casing == 'upper' else f'{protocol}_proxy': proxy_url,
                           'NODE_EXTRA_CA_CERTS': str(cert), 'TEST_PATH': '/v2/builds',
                           'TEST_API': 'graphql' if protocol == 'https' else 'rest'}
                    if casing == 'both':
                        env[f'{protocol}_proxy'.upper()] = proxy_url
                        env[f'{protocol}_proxy'] = 'http://127.0.0.1:1'
                    result = self.run_bk(f'{protocol}://api.invalid/', ['build', 'view', '123'], **env)
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(result.stdout, 'upstream response')
                    self.assertEqual(len(proxy.received), index)
                    self.assertEqual(json.loads(self.log.read_text().splitlines()[0])['status'], 200)
        # Go bypasses loopback even when NO_PROXY does not match it.
        with server() as upstream:
            result = self.run_bk(f'http://127.0.0.1:{upstream.server_port}/', ['build', 'view', '123'],
                                 HTTP_PROXY='http://127.0.0.1:1', NO_PROXY='other.invalid')
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        # Matching NO_PROXY routes directly (and fails DNS for api.invalid);
        # nonmatches still reach the proxy. Include Go's domain suffix matching.
        with server() as proxy:
            for bypass, expected in (('api.invalid', 8), ('invalid', 8), ('invalid:80', 8),
                                      ('invalid:81', 0), ('other.invalid', 0), ('*', 8)):
                result = self.run_bk('http://api.invalid/', ['api'],
                                     HTTP_PROXY=f'http://127.0.0.1:{proxy.server_port}', NO_PROXY=bypass)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
            for env, expected in (({'no_proxy': 'invalid'}, 8),
                                  ({'NO_PROXY': 'other.invalid', 'no_proxy': 'invalid'}, 0)):
                result = self.run_bk('http://api.invalid/', ['api'],
                                     HTTP_PROXY=f'http://127.0.0.1:{proxy.server_port}', **env)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        # Select this machine's routable IP without sending a packet. Using a
        # non-loopback address distinguishes a CIDR match from Go's loopback rule.
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as route:
            route.connect(('192.0.2.1', 80))
            host = route.getsockname()[0]
        with server(host='0.0.0.0') as upstream:
            for bypass, expected in ((f'{host}/32', 0), ('0.0.0.0/0', 0), ('192.0.2.1/32', 8)):
                result = self.run_bk(f'http://{host}:{upstream.server_port}/', ['api'],
                                     HTTP_PROXY='http://127.0.0.1:1', NO_PROXY=bypass)
                self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
                if expected == 0:
                    self.assertEqual(result.stdout, 'upstream response')

    def test_transport_errors_do_not_disclose_urls_or_proxy_credentials(self):
        result = self.run_bk('http://api.invalid/', ['api', 'fixture-argument-secret'],
                             HTTP_PROXY='http://fixture-proxy-user:fixture-proxy-password@127.0.0.1:1')
        self.assertEqual(result.returncode, 8, result.stdout + result.stderr)
        records = [json.loads(line) for line in self.log.read_text().splitlines()]
        self.assertEqual(records[0]['error'], 'ECONNREFUSED')
        for value in ('fixture-query-secret', 'fixture-argument-secret',
                      'fixture-proxy-user', 'fixture-proxy-password'):
            self.assertNotIn(value, self.log.read_text() + result.stdout + result.stderr)


if __name__ == '__main__':
    unittest.main()
