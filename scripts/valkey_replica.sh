#!/bin/bash
# Run on Sys3 and Sys4
PRIMARY_IP=$1  # e.g. 10.1.75.51
PRIMARY_PORT=${2:-6379}
LOCAL_PORT=${3:-6379}
if [ -z "$PRIMARY_IP" ]; then
    echo "Usage: $0 <primary_ip> [primary_port] [local_port]"
    exit 1
fi
redis-server --daemonize yes --bind 0.0.0.0 --port $LOCAL_PORT \
    --replicaof $PRIMARY_IP $PRIMARY_PORT --replica-read-only yes
