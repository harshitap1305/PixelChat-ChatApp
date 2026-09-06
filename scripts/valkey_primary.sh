#!/bin/bash
# Run on Sys2 — start Valkey PRIMARY with AOF persistence
PORT=${1:-6379}
redis-server --daemonize yes --bind 0.0.0.0 --port $PORT \
    --appendonly yes --appendfilename valkey.aof
