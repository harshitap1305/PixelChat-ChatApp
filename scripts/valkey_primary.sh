#!/bin/bash
# Run on Sys2 — start Valkey PRIMARY
# Uses AOF with appendfsync everysec: Valkey replies to clients instantly;
# a background OS thread flushes to disk once per second (max ~1s data loss).
# This satisfies persistence requirements without blocking request latency.
PORT=${1:-4000}
redis-server --daemonize yes --bind 0.0.0.0 --port $PORT \
    --protected-mode no \
    --appendonly yes \
    --appendfilename valkey.aof \
    --appendfsync everysec \
    --no-appendfsync-on-rewrite yes \
    --save "" \
    --maxclients 1000 \
    --tcp-backlog 511
