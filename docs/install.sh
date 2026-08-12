#!/bin/sh
# Install the comis-dev-crew executables from a published GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/comisai/comis-dev-crew/main/docs/install.sh | sh
#
# The archive digest is verified against the release checksums.txt before
# anything is written outside the temporary directory. A missing, unlisted, or
# mismatched digest aborts the install rather than proceeding unverified.
#
# Overrides:
#   DEVCREW_INSTALL_DIR  where the executables land (default ~/.comis-dev-crew/bin)
#   DEVCREW_LINK_DIR     where the PATH symlinks land (default ~/.local/bin, else /usr/local/bin)
#   DEVCREW_VERSION      install an exact tag instead of the latest release

set -eu

REPO="comisai/comis-dev-crew"
COMMANDS="devcrew devcrew-service devcrew-mcp devcrew-report"
INSTALL_DIR="${DEVCREW_INSTALL_DIR:-$HOME/.comis-dev-crew/bin}"
LINK_DIR="${DEVCREW_LINK_DIR:-}"
VERSION="${DEVCREW_VERSION:-}"

fail() {
	echo "install: $1" >&2
	exit 1
}

if [ -z "$LINK_DIR" ]; then
	case ":$PATH:" in
	*":$HOME/.local/bin:"*) LINK_DIR="$HOME/.local/bin" ;;
	*) LINK_DIR="/usr/local/bin" ;;
	esac
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
darwin | linux) ;;
*) fail "unsupported operating system: ${OS}. comis-dev-crew supports darwin and linux." ;;
esac

case "$ARCH" in
x86_64 | amd64) ARCH="amd64" ;;
arm64 | aarch64) ARCH="arm64" ;;
*) fail "unsupported architecture: ${ARCH}. comis-dev-crew supports amd64 and arm64." ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
	digest_of() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
	digest_of() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
	fail "no SHA-256 tool found. Install sha256sum or shasum and re-run."
fi

tag_from() {
	curl -fsSL "$1" 2>/dev/null |
		sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1
}

if [ -z "$VERSION" ]; then
	VERSION="$(tag_from "https://api.github.com/repos/${REPO}/releases/latest")" || true
fi
if [ -z "$VERSION" ]; then
	# The releases/latest endpoint answers 404 while every published release is
	# a prerelease, which is the normal state for this project. Fall back to the
	# newest release of any kind so a pre-release build stays installable.
	VERSION="$(tag_from "https://api.github.com/repos/${REPO}/releases?per_page=1")" || true
fi
if [ -z "$VERSION" ]; then
	fail "could not determine the latest release.
  comis-dev-crew is pre-release and may not have published one yet.
  Set DEVCREW_VERSION to install an exact tag, or build from source:
  https://github.com/${REPO}#install"
fi

ARCHIVE="comis-dev-crew-${VERSION}-${OS}-${ARCH}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

echo "Downloading comis-dev-crew ${VERSION} for ${OS}/${ARCH}..."
curl -fsSL "${BASE_URL}/${ARCHIVE}" -o "${WORK_DIR}/${ARCHIVE}" ||
	fail "could not download ${BASE_URL}/${ARCHIVE}"
curl -fsSL "${BASE_URL}/checksums.txt" -o "${WORK_DIR}/checksums.txt" ||
	fail "could not download the release checksums. Refusing to install unverified binaries."

EXPECTED="$(
	sed -n "s/^\([0-9a-f]\{64\}\)[[:space:]][[:space:]]*[*]\{0,1\}${ARCHIVE}\$/\1/p" \
		"${WORK_DIR}/checksums.txt" | head -n 1
)"
[ -n "$EXPECTED" ] || fail "checksums.txt has no entry for ${ARCHIVE}. Refusing to install."

ACTUAL="$(digest_of "${WORK_DIR}/${ARCHIVE}")"
if [ "$ACTUAL" != "$EXPECTED" ]; then
	fail "checksum mismatch for ${ARCHIVE}.
  expected ${EXPECTED}
  actual   ${ACTUAL}
  The download was corrupted or tampered with. Nothing was installed."
fi
echo "Checksum verified."

tar xzf "${WORK_DIR}/${ARCHIVE}" -C "$WORK_DIR" || fail "could not extract ${ARCHIVE}"
for name in $COMMANDS; do
	[ -f "${WORK_DIR}/bin/${name}" ] || fail "release archive is missing bin/${name}"
done

mkdir -p "$INSTALL_DIR" || fail "could not create the install directory: ${INSTALL_DIR}"
for name in $COMMANDS; do
	mv "${WORK_DIR}/bin/${name}" "${INSTALL_DIR}/${name}"
	chmod 755 "${INSTALL_DIR}/${name}"
done

resolve_path() { (cd "$1" 2>/dev/null && pwd -P); }
REAL_INSTALL_DIR="$(resolve_path "$INSTALL_DIR" || true)"
REAL_LINK_DIR="$(resolve_path "$LINK_DIR" || true)"

if [ -n "$REAL_INSTALL_DIR" ] && [ "$REAL_INSTALL_DIR" = "$REAL_LINK_DIR" ]; then
	echo "Install and link directories resolve to the same path; skipping symlinks."
elif [ -w "$LINK_DIR" ] || (mkdir -p "$LINK_DIR" 2>/dev/null && [ -w "$LINK_DIR" ]); then
	for name in $COMMANDS; do
		rm -f "${LINK_DIR}/${name}"
		ln -s "${INSTALL_DIR}/${name}" "${LINK_DIR}/${name}"
	done
else
	echo "Linking into ${LINK_DIR} (requires sudo)..."
	sudo mkdir -p "$LINK_DIR"
	for name in $COMMANDS; do
		sudo rm -f "${LINK_DIR}/${name}"
		sudo ln -s "${INSTALL_DIR}/${name}" "${LINK_DIR}/${name}"
	done
fi

echo "comis-dev-crew ${VERSION} installed to ${INSTALL_DIR}"
for name in $COMMANDS; do
	echo "  ${LINK_DIR}/${name} -> ${INSTALL_DIR}/${name}"
done

case ":$PATH:" in
*":$LINK_DIR:"*) ;;
*) echo "Add ${LINK_DIR} to your PATH and restart your terminal." ;;
esac

echo
echo "comis-dev-crew is pre-release. Read the status section before running it"
echo "against anything you care about: https://github.com/${REPO}#status-pre-release"
