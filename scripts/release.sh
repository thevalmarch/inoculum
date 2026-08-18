#!/bin/sh

set -eu

VERSION=${1:-v1.0.0}
MINIMUM_GO_VERSION=1.26.6

if [ "$#" -gt 1 ]; then
	echo "usage: scripts/release.sh [vMAJOR.MINOR.PATCH]" >&2
	exit 2
fi

if ! printf '%s\n' "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "version must use vMAJOR.MINOR.PATCH form" >&2
	exit 2
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPOSITORY_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)
OUTPUT_DIR="$REPOSITORY_ROOT/release"
STAGING_DIR=$(mktemp -d "${TMPDIR:-/tmp}/inoculum-release.XXXXXX")

cleanup() {
	case "$STAGING_DIR" in
		*/inoculum-release.*) rm -rf -- "$STAGING_DIR" ;;
	esac
}
trap cleanup EXIT HUP INT TERM

for command in go tar gzip zip unzip shasum; do
	if ! command -v "$command" >/dev/null 2>&1; then
		echo "required command not found: $command" >&2
		exit 1
	fi
done

GO_VERSION=$(go env GOVERSION)
GO_VERSION=${GO_VERSION#go}
if ! printf '%s\n' "$GO_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
	echo "could not determine a stable Go release version (found: $GO_VERSION)" >&2
	exit 1
fi
version_at_least() (
	current=$1
	minimum=$2
	IFS=.
	set -- $current
	current_major=$1 current_minor=$2 current_patch=$3
	set -- $minimum
	minimum_major=$1 minimum_minor=$2 minimum_patch=$3
	[ "$current_major" -gt "$minimum_major" ] ||
		{ [ "$current_major" -eq "$minimum_major" ] && [ "$current_minor" -gt "$minimum_minor" ]; } ||
		{ [ "$current_major" -eq "$minimum_major" ] && [ "$current_minor" -eq "$minimum_minor" ] && [ "$current_patch" -ge "$minimum_patch" ]; }
)
if ! version_at_least "$GO_VERSION" "$MINIMUM_GO_VERSION"; then
	echo "Go $MINIMUM_GO_VERSION or later is required for release builds (found: $GO_VERSION)" >&2
	exit 1
fi

if [ ! -f "$REPOSITORY_ROOT/go.mod" ] || [ ! -f "$REPOSITORY_ROOT/cmd/inoculum/main.go" ]; then
	echo "could not identify the Inoculum repository root" >&2
	exit 1
fi

DARWIN_NAME="inoculum_${VERSION}_darwin_arm64.tar.gz"
LINUX_NAME="inoculum_${VERSION}_linux_amd64.tar.gz"
WINDOWS_NAME="inoculum_${VERSION}_windows_amd64.zip"
LINKER_FLAGS="-s -w -X github.com/thevalmarch/inoculum/internal/version.Value=$VERSION"

DARWIN_STAGE="$STAGING_DIR/darwin-arm64"
LINUX_STAGE="$STAGING_DIR/linux-amd64"
WINDOWS_STAGE="$STAGING_DIR/windows-amd64"
PACKAGE_STAGE="$STAGING_DIR/packages"
mkdir -p "$DARWIN_STAGE" "$LINUX_STAGE" "$WINDOWS_STAGE" "$PACKAGE_STAGE"

echo "Building Inoculum $VERSION"
(
	cd "$REPOSITORY_ROOT"
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
		-trimpath -buildvcs=false -ldflags "$LINKER_FLAGS" \
		-o "$DARWIN_STAGE/inoculum" ./cmd/inoculum
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
		-trimpath -buildvcs=false -ldflags "$LINKER_FLAGS" \
		-o "$LINUX_STAGE/inoculum" ./cmd/inoculum
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
		-trimpath -buildvcs=false -ldflags "$LINKER_FLAGS" \
		-o "$WINDOWS_STAGE/inoculum.exe" ./cmd/inoculum
)

cp "$REPOSITORY_ROOT/LICENSE" "$DARWIN_STAGE/LICENSE"
cp "$REPOSITORY_ROOT/LICENSE" "$LINUX_STAGE/LICENSE"
cp "$REPOSITORY_ROOT/LICENSE" "$WINDOWS_STAGE/LICENSE"
cp "$REPOSITORY_ROOT/THIRD_PARTY_LICENSES" "$DARWIN_STAGE/THIRD_PARTY_LICENSES"
cp "$REPOSITORY_ROOT/THIRD_PARTY_LICENSES" "$LINUX_STAGE/THIRD_PARTY_LICENSES"
cp "$REPOSITORY_ROOT/THIRD_PARTY_LICENSES" "$WINDOWS_STAGE/THIRD_PARTY_LICENSES"

# Normalize archive inputs so repeated runs on the release machine do not
# change only because packaging happened at a different time.
TZ=UTC touch -t 202601010000 \
	"$DARWIN_STAGE/inoculum" "$DARWIN_STAGE/LICENSE" \
	"$DARWIN_STAGE/THIRD_PARTY_LICENSES" \
	"$LINUX_STAGE/inoculum" "$LINUX_STAGE/LICENSE" \
	"$LINUX_STAGE/THIRD_PARTY_LICENSES" \
	"$WINDOWS_STAGE/inoculum.exe" "$WINDOWS_STAGE/LICENSE" \
	"$WINDOWS_STAGE/THIRD_PARTY_LICENSES"

if [ "$("$DARWIN_STAGE/inoculum" --version)" != "inoculum $VERSION" ]; then
	echo "native release binary did not report version $VERSION" >&2
	exit 1
fi

echo "Packaging release archives"
COPYFILE_DISABLE=1 tar --format ustar --uid 0 --gid 0 --uname root --gname root \
	-C "$DARWIN_STAGE" -cf "$STAGING_DIR/darwin.tar" inoculum LICENSE THIRD_PARTY_LICENSES
gzip -n -9 -c "$STAGING_DIR/darwin.tar" > "$PACKAGE_STAGE/$DARWIN_NAME"
COPYFILE_DISABLE=1 tar --format ustar --uid 0 --gid 0 --uname root --gname root \
	-C "$LINUX_STAGE" -cf "$STAGING_DIR/linux.tar" inoculum LICENSE THIRD_PARTY_LICENSES
gzip -n -9 -c "$STAGING_DIR/linux.tar" > "$PACKAGE_STAGE/$LINUX_NAME"
(
	cd "$WINDOWS_STAGE"
	TZ=UTC COPYFILE_DISABLE=1 zip -X -q "$PACKAGE_STAGE/$WINDOWS_NAME" inoculum.exe LICENSE THIRD_PARTY_LICENSES
)

EXPECTED_UNIX_CONTENTS=$(printf '%s\n' LICENSE THIRD_PARTY_LICENSES inoculum | LC_ALL=C sort)
EXPECTED_WINDOWS_CONTENTS=$(printf '%s\n' LICENSE THIRD_PARTY_LICENSES inoculum.exe | LC_ALL=C sort)
if [ "$(tar -tzf "$PACKAGE_STAGE/$DARWIN_NAME" | LC_ALL=C sort)" != "$EXPECTED_UNIX_CONTENTS" ]; then
	echo "unexpected files in $DARWIN_NAME" >&2
	exit 1
fi
if [ "$(tar -tzf "$PACKAGE_STAGE/$LINUX_NAME" | LC_ALL=C sort)" != "$EXPECTED_UNIX_CONTENTS" ]; then
	echo "unexpected files in $LINUX_NAME" >&2
	exit 1
fi
if [ "$(unzip -Z1 "$PACKAGE_STAGE/$WINDOWS_NAME" | LC_ALL=C sort)" != "$EXPECTED_WINDOWS_CONTENTS" ]; then
	echo "unexpected files in $WINDOWS_NAME" >&2
	exit 1
fi
gzip -t "$PACKAGE_STAGE/$DARWIN_NAME"
gzip -t "$PACKAGE_STAGE/$LINUX_NAME"
unzip -tq "$PACKAGE_STAGE/$WINDOWS_NAME" >/dev/null

(
	cd "$PACKAGE_STAGE"
	shasum -a 256 "$DARWIN_NAME" "$LINUX_NAME" "$WINDOWS_NAME" > SHA256SUMS
	shasum -a 256 -c SHA256SUMS
)

case "$OUTPUT_DIR" in
	"$REPOSITORY_ROOT/release") ;;
	*)
		echo "refusing to replace unexpected output directory: $OUTPUT_DIR" >&2
		exit 1
		;;
esac
rm -rf -- "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"
cp "$PACKAGE_STAGE/$DARWIN_NAME" "$OUTPUT_DIR/"
cp "$PACKAGE_STAGE/$LINUX_NAME" "$OUTPUT_DIR/"
cp "$PACKAGE_STAGE/$WINDOWS_NAME" "$OUTPUT_DIR/"
cp "$PACKAGE_STAGE/SHA256SUMS" "$OUTPUT_DIR/"

echo "Release artifacts written to $OUTPUT_DIR"
