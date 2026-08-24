#!/bin/bash
set -e

# Clean up any existing processes
pkill -f load_balancer || true
pkill -f load_generator || true
sleep 2
rm -f results.csv 1_backend.json 3_backends.json

echo "=== Building binaries ==="
go build -o load_balancer load_balancer.go
go build -o load_generator load_generator.go

echo ""
echo "=== Experiment 1: Single Backend ==="
echo "Starting LB with 1 backend..."
./load_balancer -backends https://172.17.0.39:5000 -port 8082 &
LB_PID=$!
sleep 3

echo "LB Status:"
curl -s http://localhost:8082/lb/status
echo ""

echo "Running load generator (5000 requests, 200 concurrency, 100ms delay)..."
./load_generator -url 'http://localhost:8082/?delay=100ms' -requests 5000 -concurrency 200 -timeout 3s -experiment 1_backend

echo "LB Metrics:"
curl -s http://localhost:8082/lb/metrics
echo ""

echo "Stopping LB..."
kill $LB_PID 2>/dev/null || true
wait $LB_PID 2>/dev/null || true
sleep 3

# Make sure port 8082 is free
while ss -tlnp 2>/dev/null | grep -q ':8082 ' || netstat -tlnp 2>/dev/null | grep -q ':8082 '; do
    echo "Waiting for port 8082 to be free..."
    sleep 1
done

echo ""
echo "=== Experiment 2: Three Backends ==="
echo "Starting LB with 3 backends..."
./load_balancer -backends https://172.17.0.39:5000,https://172.17.0.40:5000,https://172.17.0.41:5000 -port 8082 &
LB_PID=$!
sleep 3

echo "LB Status:"
curl -s http://localhost:8082/lb/status
echo ""

echo "Running load generator (5000 requests, 200 concurrency, 100ms delay)..."
./load_generator -url 'http://localhost:8082/?delay=100ms' -requests 5000 -concurrency 200 -timeout 3s -experiment 3_backends

echo "LB Metrics:"
curl -s http://localhost:8082/lb/metrics
echo ""

echo "Stopping LB..."
kill $LB_PID 2>/dev/null || true
wait $LB_PID 2>/dev/null || true

echo ""
echo "=== Results ==="
cat results.csv
echo ""
echo "=== ALL EXPERIMENTS DONE ==="
