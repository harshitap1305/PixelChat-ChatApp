#!/bin/bash
# Run on Sys3 and Sys4
PRIMARY_IP=$1  # e.g. 10.1.75.51
if [ -z "$PRIMARY_IP" ]; then
    echo "Usage: $0 <primary_ip>"
    exit 1
fi
redis-server --daemonize yes --bind 0.0.0.0 --port 6379 \
    --replicaof $PRIMARY_IP 6379 --replica-read-only yes
