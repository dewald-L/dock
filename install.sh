#!/bin/sh
# Install (or update) dock and its plugins on macOS or Linux:
#
#   curl -fsSL https://raw.githubusercontent.com/dewald-L/dock/main/install.sh | sh
#
# Plugins: by default it installs the plugins the plugin index marks as
# default (dock plugin install --defaults). Pass plugin ids or Go packages to
# install those instead, or "none" for no plugins:
#
#   curl -fsSL .../install.sh | sh -s -- lazy1on1 github.com/you/plugin/dockplugin
#
# DOCK_REGISTRY points it at another plugin index (a URL or a file).
#
# What it does: installs Go into ~/.local/go if there is no usable Go
# (dock needs it to build itself with plugins), downloads dock's source
# into ~/.local/share/dock/src/dock and builds it, installs the
# plugins, links ~/.local/bin/dock and adds ~/.local/bin (and Go) to your
# shell's PATH. Running it again updates everything. No sudo needed.
set -eu

say() { printf '\033[1m==>\033[0m %s\n' "$*"; }
warn() { printf 'dock install: warning: %s\n' "$*" >&2; }
die() {
	printf 'dock install: %s\n' "$*" >&2
	exit 1
}

BIN="$HOME/.local/bin"
DATA="${DOCK_DATA_DIR:-${XDG_DATA_HOME:-$HOME/.local/share}/dock}"
GOROOT_LOCAL="$HOME/.local/go"
PATH="$BIN:$GOROOT_LOCAL/bin:$PATH"
export PATH

# ---- Go ----

# go_ok: Go 1.21+ fetches any newer toolchain dock needs by itself.
go_ok() {
	command -v go >/dev/null 2>&1 || return 1
	v=$(go env GOVERSION 2>/dev/null | sed -n 's/^go1\.\([0-9]*\).*/\1/p')
	[ -n "$v" ] && [ "$v" -ge 21 ]
}

if ! go_ok; then
	case "$(uname -s)" in
	Darwin) os=darwin ;;
	Linux) os=linux ;;
	*) die "unsupported OS $(uname -s)" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "unsupported CPU $(uname -m)" ;;
	esac
	ver=$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -n 1)
	say "installing $ver into $GOROOT_LOCAL"
	rm -rf "$GOROOT_LOCAL"
	mkdir -p "$HOME/.local"
	curl -fsSL "https://go.dev/dl/$ver.$os-$arch.tar.gz" | tar -C "$HOME/.local" -xzf -
	go_ok || die "Go install failed"
fi
GOTOOLCHAIN=auto
# Fetch dock's own modules straight from GitHub: the Go proxy can serve a
# stale @latest for a while after a push.
GOPRIVATE="${GOPRIVATE:+$GOPRIVATE,}github.com/dewald-L/*"
export GOTOOLCHAIN GOPRIVATE

# ---- dock ----

# dock is built from a source checkout (its go.mod has replace directives,
# which `go install pkg@latest` refuses). The binary sits in the checkout,
# so dock builds itself from that same source when installing plugins.
say "installing dock"
boot="$DATA/src/dock"
rm -rf "$boot"
mkdir -p "$boot" "$BIN"
curl -fsSL https://codeload.github.com/dewald-L/dock/tar.gz/refs/heads/main | tar -C "$boot" --strip-components=1 -xzf -
(cd "$boot" && go build -o dock .)

if [ "$#" -eq 0 ]; then
	say "installing the default plugins"
	if ! "$boot/dock" plugin install --defaults; then
		warn "could not install the default plugins; building dock without them"
		"$boot/dock" build
	fi
elif [ "$*" = "none" ]; then
	say "building dock"
	"$boot/dock" build
else
	for p in "$@"; do
		say "installing plugin $p"
		"$boot/dock" plugin install "$p"
	done
fi
ln -sf "$DATA/bin/dock" "$BIN/dock"

# ---- PATH ----

line="export PATH=\"\$HOME/.local/bin:\$HOME/.local/go/bin:\$PATH\""
case "${SHELL:-}" in
*/zsh) rc="$HOME/.zshrc" ;;
*/bash) if [ "$(uname -s)" = Darwin ]; then rc="$HOME/.bash_profile"; else rc="$HOME/.bashrc"; fi ;;
*/fish) rc="" ;;
*) rc="$HOME/.profile" ;;
esac
if [ -z "$rc" ]; then
	fish -c 'fish_add_path -U $HOME/.local/bin $HOME/.local/go/bin' 2>/dev/null || true
elif ! grep -qs '.local/bin:$HOME/.local/go/bin' "$rc"; then
	printf '\n# dock\n%s\n' "$line" >>"$rc"
	say "added ~/.local/bin to PATH in $rc (open a new terminal)"
fi

"$BIN/dock" plugin list
say "done: run \`dock\` (ctrl+g ? for help)"
