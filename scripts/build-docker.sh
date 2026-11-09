#!/bin/sh
# Build the Compose image without starting or replacing the running service.
set -eu
cd "$(dirname "$0")/.."
docker compose build --no-cache push
