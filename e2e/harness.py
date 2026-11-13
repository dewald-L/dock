"""Drive a TUI through a pty and keep a model of its screen.

A minimal ANSI emulator (enough for Bubble Tea's renderer): cursor moves,
erase line/screen, scroll regions are ignored, colours are dropped. It
answers the terminal queries dock sends at startup: OSC 11 (background
colour) and DSR 6n (cursor position).
"""
import fcntl
import os
import pty
import re
import select
import signal
import struct
import termios
import time

KEYS = {
    "enter": b"\r", "esc": b"\x1b", "tab": b"\t", "shift+tab": b"\x1b[Z",
    "up": b"\x1b[A", "down": b"\x1b[B", "right": b"\x1b[C", "left": b"\x1b[D",
    "backspace": b"\x7f", "ctrl+u": b"\x15", "ctrl+e": b"\x05", "ctrl+g": b"\x07",
    "ctrl+c": b"\x03", "space": b" ",
}

CSI_RE = re.compile(rb"\x1b\[([0-9;?<>=]*)([ -/]*)([@-~])")
OSC_RE = re.compile(rb"\x1b\](.*?)(?:\x07|\x1b\\)", re.S)


class Screen:
    def __init__(self, cols, rows):
        self.cols, self.rows = cols, rows
        self.reset()

    def reset(self):
        self.grid = [[" "] * self.cols for _ in range(self.rows)]
        self.x = self.y = 0
        self.saved = (0, 0)

    def resize(self, cols, rows):
        old = self.grid
        self.cols, self.rows = cols, rows
        self.grid = [[" "] * cols for _ in range(rows)]
        for y in range(min(rows, len(old))):
            for x in range(min(cols, len(old[y]))):
                self.grid[y][x] = old[y][x]
        self.x, self.y = min(self.x, cols - 1), min(self.y, rows - 1)

    def scroll(self):
        self.grid.pop(0)
        self.grid.append([" "] * self.cols)

    def put(self, ch):
        if self.x >= self.cols:
            self.x = 0
            self.newline()
        self.grid[self.y][self.x] = ch
        self.x += 1

    def newline(self):
        if self.y == self.rows - 1:
            self.scroll()
        else:
            self.y += 1

    def csi(self, params, final):
        priv = params.startswith(b"?")
        nums = [int(p) if p.isdigit() else 0 for p in params.lstrip(b"?<>=").split(b";")] if params else []
        n = nums[0] if nums and nums[0] else 1
        f = chr(final)
        if priv:
            if f in "hl" and nums and nums[0] == 1049:
                self.reset()
            return
        if f == "A":
            self.y = max(0, self.y - n)
        elif f == "B":
            self.y = min(self.rows - 1, self.y + n)
        elif f == "C":
            self.x = min(self.cols - 1, self.x + n)
        elif f == "D":
            self.x = max(0, self.x - n)
        elif f == "E":
            self.y, self.x = min(self.rows - 1, self.y + n), 0
        elif f == "F":
            self.y, self.x = max(0, self.y - n), 0
        elif f == "G":
            self.x = min(self.cols - 1, n - 1)
        elif f in "Hf":
            row = nums[0] if nums and nums[0] else 1
            col = nums[1] if len(nums) > 1 and nums[1] else 1
            self.y, self.x = min(self.rows, row) - 1, min(self.cols, col) - 1
        elif f == "d":
            self.y = min(self.rows, n) - 1
        elif f == "K":
            mode = nums[0] if nums else 0
            row = self.grid[self.y]
            rng = {0: range(self.x, self.cols), 1: range(0, self.x + 1), 2: range(self.cols)}[mode]
            for i in rng:
                row[i] = " "
        elif f == "J":
            mode = nums[0] if nums else 0
            if mode == 0:
                self.grid[self.y][self.x:] = [" "] * (self.cols - self.x)
                for y in range(self.y + 1, self.rows):
                    self.grid[y] = [" "] * self.cols
            elif mode == 1:
                for y in range(self.y):
                    self.grid[y] = [" "] * self.cols
                self.grid[self.y][: self.x + 1] = [" "] * (self.x + 1)
            else:
                self.grid = [[" "] * self.cols for _ in range(self.rows)]
        elif f == "X":
            for i in range(self.x, min(self.cols, self.x + n)):
                self.grid[self.y][i] = " "
        elif f == "P":
            row = self.grid[self.y]
            del row[self.x:self.x + n]
            row += [" "] * n
        elif f == "@":
            row = self.grid[self.y]
            for _ in range(n):
                row.insert(self.x, " ")
            del row[self.cols:]
        elif f == "L":
            for _ in range(n):
                self.grid.insert(self.y, [" "] * self.cols)
                self.grid.pop()
        elif f == "M":
            for _ in range(n):
                self.grid.pop(self.y)
                self.grid.append([" "] * self.cols)
        elif f == "S":
            for _ in range(n):
                self.scroll()
        # m (colours), r (scroll region), t, etc. are ignored

    carry = b""

    def feed(self, data):
        text = self.carry + data
        self.carry = b""
        i = 0
        while i < len(text):
            b = text[i]
            if b == 0x1b:
                m = CSI_RE.match(text, i)
                if m:
                    self.csi(m.group(1), m.group(3)[0])
                    i = m.end()
                    continue
                m = OSC_RE.match(text, i)
                if m:
                    i = m.end()
                    continue
                rest = text[i:]
                if (len(rest) < 2 or (rest[1:2] == b"[" and re.fullmatch(rb"\x1b\[[0-9;?<>=]*[ -/]*", rest))
                        or (rest[1:2] in (b"]", b"P") and b"\x07" not in rest and b"\x1b\\" not in rest)):
                    self.carry = rest  # incomplete: wait for the rest of the sequence
                    return
                if text[i + 1:i + 2] == b"7":
                    self.saved = (self.x, self.y)
                elif text[i + 1:i + 2] == b"8":
                    self.x, self.y = self.saved
                elif text[i + 1:i + 2] == b"P":  # DCS ... ST
                    end = text.find(b"\x1b\\", i)
                    i = len(text) if end < 0 else end + 2
                    continue
                i += 2
                continue
            if b == 0x0d:
                self.x = 0
            elif b == 0x0a:
                self.newline()
            elif b == 0x08:
                self.x = max(0, self.x - 1)
            elif b < 0x20:
                pass
            else:
                # decode one UTF-8 character
                n = 1 if b < 0x80 else 2 if b < 0xe0 else 3 if b < 0xf0 else 4
                if i + n > len(text):
                    self.carry = text[i:]
                    return
                ch = text[i:i + n].decode("utf-8", "replace")
                self.put(ch)
                i += n
                continue
            i += 1

    def lines(self):
        return ["".join(r).rstrip() for r in self.grid]

    def text(self):
        return "\n".join(self.lines())


class Term:
    def __init__(self, argv, env, cols=120, rows=40, cwd=None):
        self.cols, self.rows = cols, rows
        self.screen = Screen(cols, rows)
        self.raw = bytearray()
        self.pending = b""
        pid, fd = pty.fork()
        if pid == 0:
            if cwd:
                os.chdir(cwd)
            os.execvpe(argv[0], argv, env)
        self.pid, self.fd = pid, fd
        self.set_size(cols, rows)
        self.exited = None

    def set_size(self, cols, rows):
        self.cols, self.rows = cols, rows
        fcntl.ioctl(self.fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))
        self.screen.resize(cols, rows)
        try:
            os.kill(self.pid, signal.SIGWINCH)
        except ProcessLookupError:
            pass

    def _answer_queries(self, data):
        buf = self.pending + data
        if b"\x1b]11;?" in buf:
            os.write(self.fd, b"\x1b]11;rgb:0000/0000/0000\x1b\\")
            buf = buf.replace(b"\x1b]11;?", b"")
        if b"\x1b[6n" in buf:
            os.write(self.fd, b"\x1b[1;1R")
            buf = buf.replace(b"\x1b[6n", b"")
        self.pending = buf[-8:]

    def pump(self, timeout=0.05):
        end = time.time() + timeout
        while True:
            left = end - time.time()
            if left <= 0:
                return
            r, _, _ = select.select([self.fd], [], [], left)
            if not r:
                return
            try:
                data = os.read(self.fd, 65536)
            except OSError:
                self.reap()
                return
            if not data:
                self.reap()
                return
            self.raw += data
            self._answer_queries(data)
            self.screen.feed(data)

    def reap(self):
        if self.exited is None:
            try:
                pid, status = os.waitpid(self.pid, os.WNOHANG)
                if pid:
                    self.exited = os.waitstatus_to_exitcode(status)
            except ChildProcessError:
                self.exited = -1

    def send(self, *keys, delay=0.12):
        for k in keys:
            os.write(self.fd, KEYS.get(k, k.encode() if isinstance(k, str) else k))
            self.pump(delay)

    def type(self, text, delay=0.12):
        os.write(self.fd, text.encode())
        self.pump(delay)

    def text(self):
        return self.screen.text()

    def wait_for(self, needle, timeout=8.0):
        end = time.time() + timeout
        while time.time() < end:
            self.pump(0.05)
            hay = self.text()
            if (needle(hay) if callable(needle) else needle in hay):
                return hay
        raise AssertionError(f"timed out waiting for {needle!r}; screen:\n{self.text()}")

    def wait_gone(self, needle, timeout=8.0):
        return self.wait_for(lambda s: needle not in s, timeout)

    def wait_exit(self, timeout=5.0):
        end = time.time() + timeout
        while time.time() < end and self.exited is None:
            self.pump(0.05)
            self.reap()
        return self.exited

    def close(self):
        if self.exited is None:
            try:
                os.kill(self.pid, signal.SIGTERM)
            except ProcessLookupError:
                pass
            self.wait_exit(2)
        try:
            os.close(self.fd)
        except OSError:
            pass
