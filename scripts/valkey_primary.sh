#!/bin/bash
# Run on Sys2 — start Valkey PRIMARY (in-memory only, max throughput)
PORT=${1:-4000}
redis-server --daemonize yes --bind 0.0.0.0 --port $PORT \
    --protected-mode no \
    --appendonly no \
    --save "" \
    --maxclients 1000 \
    --tcp-backlog 511
