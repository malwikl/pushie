#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
mkdir -p bin
cd src
pushie_build_version=$(go run ./cmd/buildversion)
go build -mod=readonly -buildvcs=false -trimpath -ldflags="-X main.buildVersion=$pushie_build_version" -o ../bin/pushie .
printf 'Built pushie %s\n' "$pushie_build_version"
