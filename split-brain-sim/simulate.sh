#!/usr/bin/env bash
#
# Standalone entrypoint kept for compatibility with the issue instructions.
# The Docker Compose reproducer is the canonical implementation.
#
set -euo pipefail
cd "$(dirname "$0")"

if [[ "${1:-}" == "--cleanup" ]]; then
    docker compose down -v
    exit 0
fi

exec ./setup-and-simulate.sh
