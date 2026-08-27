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
#   PRIZM_REQUIRE_SIG   set to 1 to abort unless the GPG signature verifies
#
# Installs into your home directory and never calls sudo. Every download is
# checked against the release's published SHA-256 before anything is unpacked.
#
# Releases are also signed. If gpg is installed and you already trust the
# signing key, the signature is checked automatically; otherwise the checksum
# alone is used and the script says so. To demand a signature, import the key
# and set PRIZM_REQUIRE_SIG=1:
#
#   gpg --recv-keys 49B66FF00161FF5AF6587CB59083374841288B9D
#   PRIZM_REQUIRE_SIG=1 sh install.sh
#
# More than one fingerprint may be listed below: signing keys get rotated, and
# a release stays signed by whichever key was current when it was published.

set -eu

REPO="troglodytto/prizm"
BIN="prizm"

: "${PRIZM_INSTALL_DIR:=${HOME}/.local/bin}"
: "${PRIZM_VERSION:=}"
: "${PRIZM_REQUIRE_SIG:=0}"

# Fingerprints that have signed releases, newest first.
#
# Verified against this list rather than against "any key in your keyring" —
# otherwise a signature proves only that somebody signed something.
#
# A key stays listed after it is rotated out. Releases it signed are already
# published and immutable, so dropping it would mean older versions stop
# verifying for anyone installing them today.
SIGNING_KEYS="
49B66FF00161FF5AF6587CB59083374841288B9D
"

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

	# Optional, so its absence is not an error here — verify_signature decides.
	curl -fsSL -o "${tmp}/checksums.txt.asc" "${base}/checksums.txt.asc" 2>/dev/null || true
}

# ── Signature ─────────────────────────────────────────────────────────────
# Checked before the checksum, because the signature is what makes the
# checksum worth anything: an attacker who can serve you a tarball can serve
# you a matching hash just as easily.
verify_signature() {
	if [ ! -s "${tmp}/checksums.txt.asc" ]; then
		require_sig_or_warn "this release is not signed"
		return
	fi

	if ! command -v gpg >/dev/null 2>&1; then
		require_sig_or_warn "gpg is not installed"
		return
	fi

	held=""
	for fpr in ${SIGNING_KEYS}; do
		gpg --list-keys "${fpr}" >/dev/null 2>&1 && held="${held} ${fpr}"
	done
	if [ -z "${held}" ]; then
		require_sig_or_warn "no release signing key is in your keyring"
		return
	fi

	status="$(gpg --status-fd 1 --verify \
		"${tmp}/checksums.txt.asc" "${tmp}/checksums.txt" 2>/dev/null)"

	# Any listed key is acceptable: an older release is signed by whichever
	# key was current when it was cut, and that signature stays valid.
	for fpr in ${held}; do
		case "${status}" in
			*"[GNUPG:] VALIDSIG ${fpr}"*)
				say "signature ok (${fpr})"
				return ;;
		esac
	done

	die "signature is not from a known prizm release key — do not install this"
}

require_sig_or_warn() {
	if [ "${PRIZM_REQUIRE_SIG}" = "1" ]; then
		die "PRIZM_REQUIRE_SIG=1 but $1"
	fi
	say "note: signature not checked — $1"
	say "      to verify: gpg --recv-keys $(printf '%s' "${SIGNING_KEYS}" | tr -s '[:space:]' ' ')"
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
	verify_signature
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
