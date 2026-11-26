"""Opt-in disposable Gitea 1.27.3 qualification; never targets an existing forge."""
import argparse
import base64
import getpass
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


class RejectRedirects(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def run(binary, work, expected_version):
    home = work / "home"
    home.mkdir(mode=0o700)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base = f"http://127.0.0.1:{port}"
    conf = work / "app.ini"
    conf.write_text(f"""APP_NAME = Disposable Dev Plane qualification
RUN_USER = {getpass.getuser()}
RUN_MODE = prod
[server]
HTTP_ADDR = 127.0.0.1
HTTP_PORT = {port}
ROOT_URL = {base}/
DISABLE_SSH = true
START_SSH_SERVER = false
APP_DATA_PATH = {work}/data
[database]
DB_TYPE = sqlite3
PATH = {work}/gitea.db
[repository]
ROOT = {work}/repositories
[security]
INSTALL_LOCK = true
SECRET_KEY = {secrets.token_hex(32)}
[service]
DISABLE_REGISTRATION = true
[actions]
ENABLED = false
[mailer]
ENABLED = false
[log]
MODE = console
LEVEL = Warn
""")
    conf.chmod(0o600)
    # Do not inherit operator credentials or agent-controlled Git configuration.
    env = {
        "PATH": os.environ.get("PATH", os.defpath),
        "HOME": str(home),
        "USER": getpass.getuser(),
        "LOGNAME": getpass.getuser(),
        "LANG": "C.UTF-8",
        "GITEA_WORK_DIR": str(work),
        "XDG_CONFIG_HOME": str(home / ".config"),
        "GIT_CONFIG_GLOBAL": str(home / ".gitconfig"),
        "GIT_CONFIG_NOSYSTEM": "1",
    }
    cmd = [str(binary), "--work-path", str(work), "--config", str(conf)]

    def cli(*args):
        result = subprocess.run(
            cmd + list(args), env=env, cwd=work, capture_output=True,
            text=True, timeout=60, check=False,
        )
        if result.returncode != 0:
            raise RuntimeError(f"Gitea CLI failed ({result.returncode})")
        return result.stdout.strip()

    def token(name, scopes):
        value = cli("admin", "user", "generate-access-token", "--username",
                    "forgepilot", "--token-name", name, "--scopes", scopes, "--raw")
        if not value or any(ch.isspace() for ch in value):
            raise RuntimeError("unexpected token output")
        return value

    opener = urllib.request.build_opener(
        urllib.request.ProxyHandler({}), RejectRedirects()
    )
    cli("migrate")
    cli("admin", "user", "create", "--username", "forgepilot", "--random-password",
        "--email", "pilot@example.invalid", "--admin", "--must-change-password=false")
    agent = token("disposable-pilot", "write:repository,read:user,write:issue")
    bootstrap = token("disposable-bootstrap", "all")
    with (work / "server.log").open("w") as log:
        proc = subprocess.Popen(cmd + ["web"], cwd=work, env=env,
                                stdout=log, stderr=subprocess.STDOUT)
        try:
            for _ in range(80):
                if proc.poll() is not None:
                    raise RuntimeError("Gitea exited before becoming healthy")
                try:
                    with opener.open(base + "/api/v1/version", timeout=1) as response:
                        version = json.load(response)["version"]
                    if version != expected_version:
                        raise RuntimeError("Gitea version does not match the expected version")
                    print("Gitea version:", version, flush=True)
                    break
                except (urllib.error.URLError, TimeoutError):
                    time.sleep(0.25)
            else:
                raise RuntimeError("Gitea health timeout")

            def api(method, route, payload):
                req = urllib.request.Request(
                    base + "/api/v1" + route,
                    data=json.dumps(payload).encode(), method=method,
                    headers={"Authorization": "token " + bootstrap,
                             "Content-Type": "application/json"},
                )
                try:
                    with opener.open(req, timeout=10) as response:
                        data = response.read()
                        return json.loads(data) if data else {}
                except urllib.error.HTTPError as exc:
                    raise RuntimeError(f"fixture API {method} {route}: HTTP {exc.code}") from None

            api("POST", "/user/repos", {
                "name": "pilot", "private": True, "auto_init": True,
                "default_branch": "main", "description": "synthetic fixture only",
            })
            api("POST", "/repos/forgepilot/pilot/branches", {
                "new_branch_name": "agent-pilot", "old_branch_name": "main",
            })
            result = api("POST", "/repos/forgepilot/pilot/contents/agent.txt", {
                "content": base64.b64encode(b"synthetic agent change\n").decode(),
                "branch": "agent-pilot", "message": "test agent change",
            })
            head = result["commit"]["sha"]
            print("Synthetic private repository and branch seeded", flush=True)
            # Only retain Go/toolchain variables, not real forge credentials.
            keys = ("PATH", "HOME", "GOPATH", "GOMODCACHE", "GOCACHE",
                    "GOTOOLCHAIN", "CGO_ENABLED", "CC", "CXX", "LANG", "LC_ALL")
            testenv = {key: os.environ[key] for key in keys if key in os.environ}
            testenv.update(GOPROXY="off", GOSUMDB="off", GOENV="off", GOMAXPROCS="3",
                           DEV_PLANE_LIVE_GITEA_BASE=base,
                           DEV_PLANE_LIVE_GITEA_TOKEN=agent,
                           DEV_PLANE_LIVE_GITEA_HEAD=head)
            subprocess.run(
                ["go", "test", "-tags", "gitealive", "-run",
                 "^TestLiveGiteaRuntimePublication$", "-count=1", "-v", "."],
                cwd=Path(__file__).resolve().parent.parent, env=testenv,
                timeout=120, check=True,
            )
            print("LIVE GITEA RUNTIME QUALIFICATION PASSED", flush=True)
        finally:
            proc.terminate()
            try:
                proc.wait(timeout=10)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=5)
            print("Disposable Gitea server stopped", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True, type=Path,
                        help="trusted, checksum-verified local Gitea binary")
    parser.add_argument("--expected-version", default="1.27.3")
    args = parser.parse_args()
    binary = args.binary.resolve(strict=True)
    if not binary.is_file() or not os.access(binary, os.X_OK):
        parser.error("--binary must be an executable file")
    with tempfile.TemporaryDirectory(prefix="dev-plane-gitea-live-") as temp:
        run(binary, Path(temp), args.expected_version)


if __name__ == "__main__":
    main()
