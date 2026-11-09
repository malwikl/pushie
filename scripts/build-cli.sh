#!/bin/sh
# Build the remote CLI; GOOS and GOARCH may be set for cross-compilation.
set -eu
cd "$(dirname "$0")/.."
mkdir -p bin
cd src
pushie_cli_suffix=
if [ "${GOOS:-$(go env GOOS)}" = windows ]; then
    pushie_cli_suffix=.exe
fi
go build -mod=readonly -buildvcs=false -trimpath -o "../bin/pushiectl${pushie_cli_suffix}" ./cmd/pushiectl
printf 'Built bin/pushiectl%s\n' "$pushie_cli_suffix"
