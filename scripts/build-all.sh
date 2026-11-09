#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
sh scripts/build.sh
sh scripts/build-cli.sh
sh scripts/build-docker.sh
