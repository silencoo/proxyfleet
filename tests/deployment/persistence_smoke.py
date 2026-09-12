"""Verify API atomic writes and restart persistence with synthetic local data only.

--image exercises real Docker bind mounts and entrypoint permissions on Linux.
--binary runs the same API scenario natively when Docker is unavailable.
"""
import argparse
import http.server
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
import uuid


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def main():
    parser = argparse.ArgumentParser()
    target = parser.add_mutually_exclusive_group(required=True)
    target.add_argument("--image")
    target.add_argument("--binary", type=Path)
    args = parser.parse_args()
    source_body = [b""]

    class Source(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(source_body[0] if self.path == "/subscription" else b"healthy")

        def log_message(self, *_):
            pass

    source = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Source)
    thread = threading.Thread(target=source.serve_forever, daemon=True)
    thread.start()
    port = source.server_port
    node1 = f"http://synthetic-one:pass@127.0.0.1:{port}#first\n"
    node2 = f"http://synthetic-two:pass@127.0.0.1:{port}#second\n"
    source_body[0] = node1.encode()
    api_port, proxy_port = free_port(), free_port()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    token = ""

    def api(path, method="GET", body=None, etag=None):
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        if etag:
            headers["If-Match"] = etag
        data = json.dumps(body).encode() if body is not None else None
        request = urllib.request.Request(f"http://127.0.0.1:{api_port}{path}", data=data,
                                         method=method, headers=headers)
        with opener.open(request, timeout=15) as response:
            return json.load(response), response.headers

    with tempfile.TemporaryDirectory(prefix="proxyfleet-persistence-") as temp:
        root = Path(temp)
        data, logs = root / "data", root / "logs"
        data.mkdir(mode=0o700)
        logs.mkdir(mode=0o700)
        config = data / "config.yaml"
        config.write_text(f'''mode: pool
listener:
  address: 127.0.0.1
  port: {proxy_port}
management:
  enabled: true
  listen: 127.0.0.1:{api_port}
  password: synthetic-smoke-password
  probe_target: http://127.0.0.1:{port}/health
  probe_timeout: 1s
  history_enabled: true
  history_interval: 10s
  audit_file: audit.log
pool:
  runtime_state_file: runtime-state.db
nodes_file: nodes.txt
subscriptions:
  - http://127.0.0.1:{port}/subscription
subscription_refresh:
  enabled: false
  timeout: 5s
  allow_private_networks: true
  quarantine_new_nodes: false
''')
        config.chmod(0o600)
        name = "proxyfleet-smoke-" + uuid.uuid4().hex[:12]
        compose_override = root / "compose.override.json"
        compose_override.write_text(json.dumps({"services": {"proxyfleet": {
            "container_name": name, "image": args.image or "unused",
            "environment": {"PROXYFLEET_UID": str(os.getuid() or 10001),
                            "PROXYFLEET_GID": str(os.getgid() or 10001)},
            "volumes": [f"{logs}:/app/logs"],
        }}}))
        compose = ["docker", "compose", "-p", name,
                   "-f", str(Path(__file__).resolve().parents[2] / "docker-compose.yml"),
                   "-f", str(compose_override)]
        compose_env = dict(os.environ, PROXYFLEET_DATA_DIR=str(data))
        process = None
        container_started = False
        output = (root / "process.log").open("w+")

        def start():
            nonlocal process, container_started, token
            token = ""
            if args.image:
                # Use the checked-in mount configuration, so a regression back
                # to single-file mounts fails the actual settings/cache writes.
                container_started = True
                subprocess.run(compose + ["up", "-d", "--no-build", "--pull", "never"],
                               env=compose_env, check=True, stdout=subprocess.DEVNULL)
            else:
                process = subprocess.Popen([str(args.binary.resolve()), "--config", str(config)],
                                           cwd=root, stdout=output, stderr=subprocess.STDOUT, umask=0o077)
            deadline = time.monotonic() + 30
            while True:
                try:
                    result, _ = api("/api/auth", "POST", {"password": "synthetic-smoke-password"})
                    token = result["token"]
                    return
                except (OSError, urllib.error.URLError):
                    if time.monotonic() >= deadline:
                        raise
                    time.sleep(0.1)

        def stop():
            nonlocal process, container_started
            if container_started:
                subprocess.run(compose + ["down", "--timeout", "15"], env=compose_env,
                               check=True, stdout=subprocess.DEVNULL)
                container_started = False
            if process is not None:
                process.terminate()
                try:
                    process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise
                process = None

        try:
            start()
            _, headers = api("/api/settings")
            api("/api/settings", "PUT", {"external_ip": "203.0.113.25"}, headers["ETag"])
            assert "203.0.113.25" in config.read_text(), "settings atomic save did not reach host"
            source_body[0] = (node1 + node2).encode()
            result, _ = api("/api/subscription/refresh", "POST")
            assert result["node_count"] == 2, result
            assert "synthetic-two" in (data / "nodes.txt").read_text(), "node cache save did not reach host"
            # Allow the minimum configured history interval to write a sample.
            deadline = time.monotonic() + 15
            while not (data / "monitor-history.json").exists():
                assert time.monotonic() < deadline, "history was not persisted"
                time.sleep(0.1)
            stop()
            for filename in ("config.yaml", "nodes.txt", "runtime-state.db", "audit.log", "monitor-history.json"):
                path = data / filename
                assert path.is_file() and path.stat().st_size, f"missing persisted {filename}"
                assert path.stat().st_mode & 0o077 == 0, f"insecure permissions on {filename}"
            before = {p.name: p.read_bytes() for p in data.iterdir() if p.is_file()}
            # A new container/process using the same mount must retain settings,
            # nodes and audit events, rather than bootstrap a fresh configuration.
            start()
            settings, _ = api("/api/settings")
            assert settings["external_ip"] == "203.0.113.25", settings
            nodes, _ = api("/api/nodes")
            assert nodes["summary"]["total_nodes"] == 2, nodes
            assert (data / "audit.log").read_bytes().startswith(before["audit.log"])
            stop()
            print("Persistence smoke passed: settings, subscription cache, SQLite, audit, history, and restart")
        except BaseException:
            if container_started:
                subprocess.run(["docker", "logs", name], check=False)
            else:
                output.flush()
                print((root / "process.log").read_text())
            raise
        finally:
            stop()
            output.close()
            source.shutdown()
            source.server_close()
            thread.join()


if __name__ == "__main__":
    main()
