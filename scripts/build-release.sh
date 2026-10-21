#!/bin/sh
# Builds every release archive into dist/ with SHA256SUMS, agents.json and the
# installers. Usage: scripts/build-release.sh <version>
set -eu
version="${1:?usage: scripts/build-release.sh <version>}"
case "$version" in
*[!0-9A-Za-z.-]*) echo "invalid version: $version" >&2; exit 1 ;;
esac

rm -rf dist
mkdir -p dist/stage
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
	os="${target%/*}"
	arch="${target#*/}"
	name="empirio_${os}_${arch}"
	stage="dist/stage/$name"
	bin=empirio
	[ "$os" = windows ] && bin=empirio.exe
	mkdir -p "$stage"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
		-ldflags "-s -w -X main.version=$version" -o "$stage/$bin" ./cmd/empirio
	cp LICENSE README.md "$stage/"
	if [ "$os" = windows ]; then
		(cd "$stage" && zip -q -X "../../$name.zip" "$bin" LICENSE README.md)
	else
		tar -C "$stage" -czf "dist/$name.tar.gz" --owner=0 --group=0 --numeric-owner "$bin" LICENSE README.md
	fi
done

# The documentation's agent table is generated from this file.
host="$(go env GOOS)_$(go env GOARCH)"
"dist/stage/empirio_$host/empirio" agents --json >dist/agents.json
cp install.sh install.ps1 dist/
rm -rf dist/stage
(cd dist && sha256sum -- * >SHA256SUMS)
ls -l dist
