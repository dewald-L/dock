"""End-to-end tests for the plugin index: drive real dock binaries through a
pty and run install.sh, all in sandboxes.

    python3 -I e2e/run.py [--keep] [--show] [--only cli,tui,offline,install,update]

Never touches the real config, data, cache or the real index: every run sets
HOME, DOCK_CONFIG_DIR, DOCK_DATA_DIR and XDG_CACHE_HOME to temp dirs, and
DOCK_REGISTRY to a fixture file (or a local HTTP server serving it). Builds
use this checkout as dock's source (config "source"). Go's module and build
caches stay the real ones, so builds are fast; plugin modules come from the
network (lazy1on1@latest).

--show prints the Plugins screen at 60x20 and 120x40 for a check by eye.
harness.py is a copy of lazysheet's e2e/harness.py.
"""
import http.server
import io
import json
import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import threading

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(HERE)
sys.path.insert(0, HERE)
from harness import Term  # noqa: E402

LAZY1ON1 = {
    "id": "lazy1on1",
    "name": "lazy1on1",
    "description": "Continuous 1:1 notes: people, meetings, goals, action items",
    "kind": "native",
    "package": "github.com/dewald-L/lazy1on1/dockplugin",
    "version": "",
    "repo": "https://github.com/dewald-L/lazy1on1",
    "tags": ["notes", "work"],
}
HELLO = {
    "id": "hello",
    "name": "Hello",
    "description": "Says hello (exec fixture)",
    "kind": "exec",
    "command": ["echo", "hello"],
    "requires": "needs echo on PATH",
}
FIXTURE = {"version": 1, "plugins": [LAZY1ON1, HELLO]}

passed = []
KEEP = "--keep" in sys.argv
SHOW = "--show" in sys.argv


def check(cond, what, screen=None):
    if not cond:
        raise AssertionError(what + ("\n--- screen ---\n" + screen if screen else ""))
    passed.append(what)
    print(f"  ok  {what}", flush=True)


def go_env(name):
    return subprocess.run(["go", "env", name], capture_output=True, text=True, check=True).stdout.strip()


GO_ENV = {k: go_env(k) for k in ("GOPATH", "GOCACHE", "GOMODCACHE")}


class Sandbox:
    """A temp HOME with dock's dirs, a fixture index and a dev dock binary."""

    roots = []

    def __init__(self, name, fixture=FIXTURE):
        self.root = tempfile.mkdtemp(prefix=f"dock-e2e-{name}-")
        Sandbox.roots.append(self.root)
        self.home = os.path.join(self.root, "home")
        os.makedirs(self.home)
        self.registry = os.path.join(self.root, "index", "plugins.json")
        os.makedirs(os.path.dirname(self.registry))
        with open(self.registry, "w") as f:
            json.dump(fixture, f, indent=2)
        self.env = {
            "HOME": self.home,
            "PATH": os.environ["PATH"],
            "TERM": "xterm-256color",
            "LANG": "C.UTF-8",
            "DOCK_CONFIG_DIR": os.path.join(self.root, "config"),
            "DOCK_DATA_DIR": os.path.join(self.root, "data"),
            "XDG_CACHE_HOME": os.path.join(self.root, "cache"),
            "DOCK_REGISTRY": self.registry,
            "DOCK_NO_UPDATE_CHECK": "1",  # the update scenario turns it back on
            **GO_ENV,
        }
        os.makedirs(self.env["DOCK_CONFIG_DIR"])
        with open(os.path.join(self.env["DOCK_CONFIG_DIR"], "config.json"), "w") as f:
            json.dump({"source": REPO}, f)
        self.dev = os.path.join(self.root, "bin", "dock-dev")
        subprocess.run(["go", "build", "-o", self.dev, "."], cwd=REPO, check=True)
        self.built = os.path.join(self.env["DOCK_DATA_DIR"], "bin", "dock")

    def run(self, *args, binary=None, check_rc=True):
        p = subprocess.run([binary or self.dev, *args], env=self.env, capture_output=True, text=True, timeout=600)
        if check_rc and p.returncode != 0:
            raise AssertionError(f"{' '.join(args)} failed ({p.returncode}):\n{p.stdout}\n{p.stderr}")
        return p

    def term(self, cols=120, rows=40):
        return Term([self.dev], self.env, cols=cols, rows=rows, cwd=self.home)


class Server:
    """Serves a directory over HTTP on 127.0.0.1; stop() takes it offline."""

    def __init__(self, directory):
        handler = lambda *a, **k: http.server.SimpleHTTPRequestHandler(*a, directory=directory, **k)  # noqa: E731
        http.server.SimpleHTTPRequestHandler.log_message = lambda *a: None
        self.httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}/plugins.json"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def stop(self):
        self.httpd.shutdown()
        self.httpd.server_close()


def open_plugins(t):
    t.wait_for("dock", 10)
    t.send("ctrl+g", "m")
    return t.wait_for("Available", 10)


def show(t, label):
    if SHOW:
        print(f"\n----- {label} ({t.cols}x{t.rows}) -----")
        for line in t.text().split("\n"):
            print("|" + line.ljust(t.cols) + "|")
        print()


# ---- scenarios ----


def scenario_cli():
    print("cli: search and install by id")
    sb = Sandbox("cli")
    out = sb.run("plugin", "search").stdout
    check("lazy1on1" in out and "Continuous 1:1 notes" in out and "hello" in out, "search lists lazy1on1 and hello")
    check("installed" not in out, "search: nothing marked installed yet")
    out = sb.run("plugin", "search", "1:1").stdout
    check("lazy1on1" in out and "hello" not in out.split("\n", 1)[1], "search filters by term")

    out = sb.run("plugin", "info", "lazy1on1").stdout
    check("not installed" in out and LAZY1ON1["package"] in out, "info works for an index entry that isn't installed")

    p = sb.run("plugin", "install", "lazy1on1")
    check("built " + sb.built in p.stdout, "install lazy1on1 builds dock")
    out = sb.run("plugin", "list", binary=sb.built).stdout
    check("lazy1on1" in out and "native" in out, "the built dock lists lazy1on1")
    out = sb.run("plugin", "search").stdout
    row = [l for l in out.splitlines() if l.startswith("lazy1on1")][0]
    check(row.rstrip().endswith("installed"), "search marks lazy1on1 installed")

    p = sb.run("plugin", "install", "hello")
    check("added hello" in p.stdout and "requires: needs echo on PATH" in p.stdout, "install of an exec entry adds it and prints requires")
    p = sb.run("plugin", "install", "nope", check_rc=False)
    check(p.returncode != 0 and 'no plugin "nope" in the index' in p.stderr, "unknown id is a clear error")

    # a future index format
    with open(sb.registry, "w") as f:
        json.dump({"version": 2, "plugins": {"x": 1}}, f)
    p = sb.run("plugin", "search", check_rc=False)
    check(p.returncode != 0 and "update dock" in p.stderr, "index version 2 asks to update dock")


def scenario_tui():
    print("tui: Available section and i to install")
    sb = Sandbox("tui")
    t = sb.term()
    try:
        s = open_plugins(t)
        s = t.wait_for("lazy1on1", 10)
        check("Available" in s and "Continuous 1:1 notes" in s, "Plugins screen shows lazy1on1 under Available", s)
        show(t, "Plugins screen")
        t.send("j")  # plugins → lazy1on1
        s = t.wait_for("not installed · i to install")
        show(t, "an Available entry selected")
        t.send("i")
        s = t.wait_for("press Enter to return", 600)
        check("building dock with 1 native plugin" in s or "built" in s, "i runs dock plugin install with the build output visible", s)
        t.send("enter")
        s = t.wait_for("restart dock to load it", 15)
        check("installed lazy1on1: restart dock to load it" in s, "notice after the install", s)
        show(t, "after install")
        t.send("ctrl+g", "q")
        t.wait_exit(5)
    finally:
        t.close()
    out = sb.run("plugin", "list", binary=sb.built).stdout
    check("lazy1on1" in out, "the screen's install built a dock with lazy1on1")

    # the new binary loads it: Available no longer lists it
    t = Term([sb.built], sb.env, cols=120, rows=40, cwd=sb.home)
    try:
        s = open_plugins(t)
        s = t.wait_for("Hello", 10)
        avail = s.split("Available", 1)[1]
        check("lazy1on1" not in avail.split("Hello")[0] and "lazy1on1" in s.split("Available")[0],
              "after a restart lazy1on1 is installed, not available", s)
    finally:
        t.close()


def scenario_offline():
    print("offline: cached index")
    sb = Sandbox("offline")
    srv = Server(os.path.dirname(sb.registry))
    sb.env["DOCK_REGISTRY"] = srv.url
    out = sb.run("plugin", "search").stdout
    check("lazy1on1" in out, "search over HTTP fills the cache")
    check(os.path.exists(os.path.join(sb.env["XDG_CACHE_HOME"], "dock", "index.json")), "cache file in XDG_CACHE_HOME")
    srv.stop()

    for cols, rows in ((120, 40), (60, 20)):
        t = sb.term(cols, rows)
        try:
            s = open_plugins(t)
            s = t.wait_for("(cached", 10)
            check("lazy1on1" in s, f"{cols}x{rows}: network off, the list comes from the cache", s)
            t.send("r")  # bypasses the cache: the fetch fails, the old copy stays
            s = t.wait_for("refresh failed", 10)
            check("(cached" in s and "lazy1on1" in s, f"{cols}x{rows}: r with the network off keeps the cached list", s)
            show(t, "offline after r")
            lines = s.split("\n")
            check(len(lines) == rows and all(len(l) <= cols for l in lines), f"{cols}x{rows}: screen fits")
        finally:
            t.close()

    # no cache and no network: a clear error in the section, dock still works
    os.remove(os.path.join(sb.env["XDG_CACHE_HOME"], "dock", "index.json"))
    t = sb.term()
    try:
        s = open_plugins(t)
        s = t.wait_for("could not load the plugin index", 15)
        check("Plugins" in s, "no cache and no network: error shown in the section", s)
        show(t, "no cache, offline")
    finally:
        t.close()


def scenario_install_sh():
    print("install.sh in a sandbox HOME")

    def install(name, *args, registry=None):
        sb = Sandbox("install-" + name)
        env = {
            "HOME": sb.home,
            "PATH": fake_curl(sb.root) + ":" + os.environ["PATH"],
            "SHELL": "/bin/sh",
            "LANG": "C.UTF-8",
            "DOCK_REGISTRY": registry or sb.registry,
            **GO_ENV,
        }
        p = subprocess.run(["sh", os.path.join(REPO, "install.sh"), *args], env=env, capture_output=True, text=True,
                           timeout=900)
        if p.returncode != 0:
            raise AssertionError(f"install.sh {' '.join(args)} failed:\n{p.stdout}\n{p.stderr}")
        dock = os.path.join(sb.home, ".local", "bin", "dock")
        listing = subprocess.run([dock, "plugin", "list"], env=env, capture_output=True, text=True, check=True).stdout
        cfg_path = os.path.join(sb.home, ".config", "dock", "config.json")
        cfg = json.load(open(cfg_path)) if os.path.exists(cfg_path) else {}
        return p, listing, cfg

    # the index is unreachable here: with no arguments install.sh must not need it
    p, listing, cfg = install("plain", registry="/nonexistent/plugins.json")
    check("add it to your PATH" not in p.stdout and "not this build" not in p.stdout,
          "install.sh output has no misleading PATH hint", p.stdout)
    check("lazy1on1" not in listing and "hello" not in listing and not cfg.get("native") and not cfg.get("exec"),
          "no arguments: dock alone, no plugins", listing)
    check("dock plugin search" in p.stdout, "install.sh says how to find plugins", p.stdout)

    p, listing, cfg = install("args", "github.com/dewald-L/dock/examples/scratch", "hello", "lazy1on1")
    check("scratch" in listing and "hello" in listing and "lazy1on1" in listing,
          "arguments (a package and index ids) are installed", listing)

    p, listing, cfg = install("none", "none")
    check("lazy1on1" not in listing and "hello" not in listing and not cfg.get("native"), "none installs no plugins", listing)


def fake_curl(root, sha=None):
    """A fake curl for install.sh: serves this checkout as dock's tarball and,
    with sha, answers GitHub's commit API with it. Refuses everything else."""
    bin_dir = os.path.join(root, "fakebin")
    os.makedirs(bin_dir)
    api = f'*api.github.com/repos/dewald-L/dock/commits/main*) echo {sha} ;;\n' if sha else ""
    script = (
        "#!/bin/sh\n"
        'case "$*" in\n'
        + api +
        f'*codeload.github.com/dewald-L/dock/*) tar -C "{REPO}" --exclude=./.git --exclude=./dock '
        "--exclude=__pycache__ --transform 's,^\\.,dock-main,' -czf - . ;;\n"
        '*) echo "fake curl: refusing $*" >&2; exit 1 ;;\n'
        "esac\n"
    )
    path = os.path.join(bin_dir, "curl")
    with open(path, "w") as f:
        f.write(script)
    os.chmod(path, 0o755)
    return bin_dir


class UpdateServer:
    """Answers like GitHub for dock update: main is at sha, and its tarball
    is this checkout plus an UPDATE_MARKER file."""

    def __init__(self, sha):
        buf = io.BytesIO()
        with tarfile.open(fileobj=buf, mode="w:gz") as tf:
            def skip(ti):
                base = ti.name.split("/", 1)[1] if "/" in ti.name else ""
                return None if base.startswith((".git", "dock-")) or base == "dock" or "__pycache__" in ti.name else ti
            tf.add(REPO, arcname=f"dock-{sha}", filter=skip)
            marker = b"updated\n"
            ti = tarfile.TarInfo(f"dock-{sha}/UPDATE_MARKER")
            ti.size = len(marker)
            tf.addfile(ti, io.BytesIO(marker))
        tarball = buf.getvalue()

        class H(http.server.BaseHTTPRequestHandler):
            def log_message(self, *a):
                pass

            def do_GET(self):
                if self.path == "/repos/dewald-L/dock/commits/main":
                    body = sha.encode()
                elif self.path == f"/dewald-L/dock/tar.gz/{sha}":
                    body = tarball
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        self.httpd = http.server.ThreadingHTTPServer(("127.0.0.1", 0), H)
        self.url = f"http://127.0.0.1:{self.httpd.server_address[1]}"
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()

    def stop(self):
        self.httpd.shutdown()
        self.httpd.server_close()


def scenario_update():
    print("update: install.sh records the commit, dock update replaces it")
    sha_a, sha_b = "a" * 40, "b" * 40
    sb = Sandbox("update")
    env = {
        "HOME": sb.home,
        "PATH": fake_curl(sb.root, sha_a) + ":" + os.environ["PATH"],
        "SHELL": "/bin/sh",
        "LANG": "C.UTF-8",
        "TERM": "xterm-256color",
        "DOCK_REGISTRY": sb.registry,
        **GO_ENV,
    }
    p = subprocess.run(["sh", os.path.join(REPO, "install.sh"), "lazy1on1"], env=env, capture_output=True, text=True,
                       timeout=900)
    if p.returncode != 0:
        raise AssertionError(f"install.sh failed:\n{p.stdout}\n{p.stderr}")
    src = os.path.join(sb.home, ".local", "share", "dock", "src", "dock")
    version = lambda: open(os.path.join(src, ".dock-version")).read().strip()  # noqa: E731
    check(version() == sha_a, "install.sh records the commit it installed")

    dock = os.path.join(sb.home, ".local", "bin", "dock")
    srv = UpdateServer(sha_b)
    env["DOCK_UPDATE_SERVER"] = srv.url
    try:
        out = subprocess.run([dock, "update", "--check"], env=env, capture_output=True, text=True, timeout=120).stdout
        check(f"dock: {sha_a[:12]} → {sha_b[:12]}" in out and version() == sha_a,
              "dock update --check shows the new commit and changes nothing", out)

        t = Term([dock], env, cols=120, rows=40, cwd=sb.home)
        try:
            s = t.wait_for("update available", 30)
            check("ctrl+g : update dock" in s or "0.2.0 · update available" in s, "the TUI says an update is available", s)
            show(t, "update available")
            t.send("ctrl+g", ":")
            t.type("update dock")
            s = t.wait_for("available: dock", 5)
            t.send("enter")
            s = t.wait_for("press Enter to return", 600)
            check("updated dock" in s, "the palette's update dock runs dock update in the terminal", s)
            t.send("enter")
            s = t.wait_for("update finished: restart dock", 15)
            check("update available" not in s, "after the update the notice is gone", s)
            t.send("ctrl+g", "q")
            t.wait_exit(5)
        finally:
            t.close()
        check(version() == sha_b and os.path.exists(os.path.join(src, "UPDATE_MARKER")), "the source is the new commit")
        check(not os.path.exists(src + ".old") and not os.path.exists(src + ".new"), "no old or partial trees left")
        out = subprocess.run([dock, "plugin", "list"], env=env, capture_output=True, text=True, check=True).stdout
        check("lazy1on1" in out, "the rebuilt dock still has its plugins", out)
        out = subprocess.run([dock, "update"], env=env, capture_output=True, text=True, timeout=300).stdout
        check("everything is up to date" in out, "a second dock update has nothing to do", out)
    finally:
        srv.stop()


SCENARIOS = {"cli": scenario_cli, "tui": scenario_tui, "offline": scenario_offline, "install": scenario_install_sh,
             "update": scenario_update}


def main():
    only = None
    if "--only" in sys.argv:
        only = sys.argv[sys.argv.index("--only") + 1].split(",")
    try:
        for name, f in SCENARIOS.items():
            if only is None or name in only:
                f()
        print(f"\nall {len(passed)} checks passed")
    finally:
        if KEEP:
            print("kept:", *Sandbox.roots)
        else:
            for r in Sandbox.roots:
                shutil.rmtree(r, ignore_errors=True)


if __name__ == "__main__":
    main()
