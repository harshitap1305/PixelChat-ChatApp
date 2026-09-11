#!/usr/bin/env bash
# start_backend.sh
# ==================
# Smart backend launcher for all 3 backend machines.
#
# On Sys2 (DB host):
#   Just run normally — uses local db.py + local chat.db
#
# On Sys3 / Sys4 (DB clients):
#   Set DB_PROXY_URL in .env → this script swaps in db_client.py as the
#   "db" module so server.py talks to Sys2's proxy instead of a local file.
#
# Usage (in your tmux pane, from the project root):
#   bash start_backend.sh
#
# Make executable:
#   chmod +x start_backend.sh

set -e

# Load .env variables
if [ -f .env ]; then
    export $(grep -v '^#' .env | grep -v '^$' | xargs)
fi

cd "$(dirname "$0")"

export BACKEND_NAME="${BACKEND_NAME:-backend}"
PORT="${PORT:-5000}"

echo "========================================================"
echo "  Chat App Backend — $BACKEND_NAME"
echo "  Internal port: $PORT"
echo "========================================================"

if [ -n "$DB_PROXY_URL" ]; then
    echo "  DB mode: REMOTE proxy at $DB_PROXY_URL"
    echo "  → Using db_client.py (HTTP calls to Sys2)"
    echo "========================================================"

    # Create a temporary directory that shadows 'db' with db_client
    TMPDIR_SHIM=$(mktemp -d)
    trap "rm -rf $TMPDIR_SHIM" EXIT

    # Copy db_client.py into the shim dir AS db.py
    # server.py does `import db` — Python will find this shim first
    cp server/db_client.py "$TMPDIR_SHIM/db.py"

    # Run server.py with the shim at the front of the Python path
    PYTHONPATH="$TMPDIR_SHIM:${PYTHONPATH:-}" python3 -m server.server
else
    echo "  DB mode: LOCAL SQLite at ${DB_PATH:-server/chat.db}"
    echo "========================================================"
    python3 -m server.server
fi
