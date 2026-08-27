#!/bin/sh
#
# Install prizm.
#
#   curl -fsSL https://raw.githubusercontent.com/troglodytto/prizm/main/install.sh | sh
#
# Or, if piping a script from the internet into a shell makes you uneasy —
# and it reasonably might — read it first:
#
#   curl -fsSLO https://raw.githubusercontent.com/troglodytto/prizm/main/install.sh
#   less install.sh && sh install.sh
#
# Environment:
#   PRIZM_VERSION       version to install (default: the latest release)
#   PRIZM_INSTALL_DIR   where to put the binary (default: ~/.local/bin)
#
# Installs into your home directory and never calls sudo. Every download is
# checked against the release's published SHA-256 before anything is unpacked.

set -eu

REPO="troglodytto/prizm"
BIN="prizm"

: "${PRIZM_INSTALL_DIR:=${HOME}/.local/bin}"
: "${PRIZM_VERSION:=}"

tmp=""
cleanup() { [ -n "${tmp}" ] && rm -rf "${tmp}"; }
trap cleanup EXIT INT TERM

die() { printf '\nerror: %s\n' "$1" >&2; exit 1; }
say() { printf '%s\n' "$1"; }

need() {
	command -v "$1" >/dev/null 2>&1 || die "$1 is required but not installed"
}

# ── What are we running on? ───────────────────────────────────────────────
detect_platform() {
	os="$(uname -s)"
	arch="$(uname -m)"

	case "${os}" in
		Linux)  os="linux" ;;
		Darwin) os="darwin" ;;
		MINGW*|MSYS*|CYGWIN*|Windows_NT)
			die "Windows is not supported yet — see ${REPO} for why, and to say you want it" ;;
		*) die "unsupported operating system: ${os}" ;;
	esac

	case "${arch}" in
		x86_64|amd64)  arch="amd64" ;;
		aarch64|arm64) arch="arm64" ;;
		*) die "unsupported architecture: ${arch}" ;;
	esac

	platform="${os}_${arch}"
}

# ── Which version? ────────────────────────────────────────────────────────
latest_version() {
	# The redirect on /releases/latest names the tag, which avoids both an API
	# call and the rate limit that comes with it on shared networks.
	url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' \
		"https://github.com/${REPO}/releases/latest" 2>/dev/null)" ||
		die "could not reach GitHub to find the latest release"

	# With no releases published, GitHub redirects to the index instead of a
	# tag, so the tail is "releases" rather than "vX.Y.Z".
	version="${url##*/}"
	case "${version}" in
		v*) ;;
		releases) die "no releases published yet — install with: go install github.com/${REPO}@latest" ;;
		*) die "could not work out the latest version (got '${version}')" ;;
	esac
}

# ── Fetch, verify, unpack ─────────────────────────────────────────────────
download() {
	archive="${BIN}_${version}_${platform}.tar.gz"
	base="https://github.com/${REPO}/releases/download/${version}"

	say "downloading ${archive}"
	curl -fsSL -o "${tmp}/${archive}" "${base}/${archive}" ||
		die "no build for ${platform} in ${version} — see https://github.com/${REPO}/releases"

	curl -fsSL -o "${tmp}/checksums.txt" "${base}/checksums.txt" ||
		die "could not download checksums; refusing to install unverified"
}

verify() {
	expected="$(grep " ${archive}\$" "${tmp}/checksums.txt" | awk '{print $1}')"
	[ -n "${expected}" ] || die "${archive} is not listed in checksums.txt"

	if command -v sha256sum >/dev/null 2>&1; then
		actual="$(sha256sum "${tmp}/${archive}" | awk '{print $1}')"
	elif command -v shasum >/dev/null 2>&1; then
		actual="$(shasum -a 256 "${tmp}/${archive}" | awk '{print $1}')"
	else
		die "need sha256sum or shasum to verify the download"
	fi

	[ "${expected}" = "${actual}" ] ||
		die "checksum mismatch — expected ${expected}, got ${actual}"
	say "checksum ok"
}

install_binary() {
	tar -xzf "${tmp}/${archive}" -C "${tmp}" || die "could not unpack ${archive}"

	extracted="${tmp}/${BIN}_${version}_${platform}/${BIN}"
	[ -f "${extracted}" ] || die "the archive did not contain ${BIN}"

	mkdir -p "${PRIZM_INSTALL_DIR}" ||
		die "could not create ${PRIZM_INSTALL_DIR}"

	# Install to a temp name and rename, so a half-written file never sits
	# where a working binary used to be.
	chmod 0755 "${extracted}"
	mv "${extracted}" "${PRIZM_INSTALL_DIR}/.${BIN}.new" ||
		die "could not write to ${PRIZM_INSTALL_DIR}"
	mv "${PRIZM_INSTALL_DIR}/.${BIN}.new" "${PRIZM_INSTALL_DIR}/${BIN}"
}

check_path() {
	case ":${PATH}:" in
		*":${PRIZM_INSTALL_DIR}:"*) return 0 ;;
	esac

	say ""
	say "${PRIZM_INSTALL_DIR} is not on your PATH. Add it:"
	say ""
	case "${SHELL##*/}" in
		fish) say "  fish_add_path ${PRIZM_INSTALL_DIR}" ;;
		zsh)  say "  echo 'export PATH=\"${PRIZM_INSTALL_DIR}:\$PATH\"' >> ~/.zshrc" ;;
		*)    say "  echo 'export PATH=\"${PRIZM_INSTALL_DIR}:\$PATH\"' >> ~/.bashrc" ;;
	esac
}

main() {
	need curl
	need tar

	detect_platform

	if [ -z "${PRIZM_VERSION}" ]; then
		latest_version
	else
		version="${PRIZM_VERSION}"
		case "${version}" in v*) ;; *) version="v${version}" ;; esac
	fi

	say "installing prizm ${version} for ${platform}"

	tmp="$(mktemp -d)"
	download
	verify
	install_binary

	say ""
	say "installed ${PRIZM_INSTALL_DIR}/${BIN}"
	"${PRIZM_INSTALL_DIR}/${BIN}" --version || true
	check_path
	say ""
	say "next:  prizm init <group>     docs: https://github.com/${REPO}"
}

main "$@"
