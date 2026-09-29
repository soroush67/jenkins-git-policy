#!/usr/bin/env bash
# Stop the lab. Data volumes are kept; `./down.sh --purge` also deletes them.
set -euo pipefail
cd "$(dirname "$0")"
# docker compose plugin, or the standalone docker-compose binary
if docker compose version >/dev/null 2>&1; then DC=(docker compose); else DC=(docker-compose); fi
if [ "${1:-}" = --purge ]; then
    "${DC[@]}" down -v
    rm -f .gitlab-token
    echo "lab stopped and volumes deleted"
else
    "${DC[@]}" down
    echo "lab stopped (volumes kept; ./down.sh --purge deletes them)"
fi
