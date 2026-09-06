#!/bin/bash
# Run on Sys2 — start Valkey PRIMARY with AOF persistence
redis-server --daemonize yes --bind 0.0.0.0 --port 6379 \
    --appendonly yes --appendfilename valkey.aof
