# Load Balancer — Deployment Guide

## System Overview

| Machine | Role | Internal Port | External Port | Notes |
|---------|------|:---:|:---:|------|
| **Sys1** | Frontend + Go Load Balancer | `3000` / `5000` | `3269` / `5269` | Your machine |
| **Sys2** | Python Backend-1 + **DB Host** + DB Proxy | `5000` / `6000` | `5270` / `6270` | Hosts `chat.db` |
| **Sys3** | Python Backend-2 | `5000` | `5271` | Points to Sys2 DB |
| **Sys4** | Python Backend-3 | `5000` | `5272` | Points to Sys2 DB |
| **Local PC** | Load Generator | — | — | Sends load to LB |

**Shared IP:** `10.1.75.51`

> **DB Proxy** — instead of sshfs, Sys2 runs a small HTTP service (`db_proxy_server.py`)
> that exposes the SQLite database over HTTPS. Sys3 and Sys4 call it like a regular API.
> No filesystem mounts. No SSH tunnels. Just HTTP.

---

## Architecture

```
Browser → https://10.1.75.51:3269
              │
              │  (Frontend JS uses BACKEND_PORT=5269)
              ▼
    ┌─────────────────────────┐
    │   Go Load Balancer      │  ← Sys1, internal :5000, external :5269
    │   Round-Robin + Health  │
    └──────┬──────────┬───────┘
           │          │          │
           ▼          ▼          ▼
      :5270        :5271       :5272
    Backend-1    Backend-2   Backend-3
    (Sys2)       (Sys3)      (Sys4)
       │             │           │
       │             └─────┬─────┘
       │                   │ HTTP (db_client.py)
       ▼                   ▼
  local chat.db  ←  DB Proxy :6000
  (Sys2 is DB host)
```

---

## Prerequisites (all machines)

```bash
# Python deps
pip install -r server/requirements.txt

# Check Git is up to date on all machines
git pull origin main
```

---

## STEP 1 — Install Go on Sys1

```bash
sudo apt update && sudo apt install golang-go -y
go version    # should show go1.21+
```

---

## STEP 2 — Build Go binaries on Sys1

```bash
cd ~/group-chat-app

# Build Load Balancer
cd load_balancer && go build -o load_balancer . && cd ..

# Build Load Generator (used for experiments)
cd load_generator && go build -o load_generator . && cd ..

echo "✅ Binaries ready"
```

---

## STEP 3 — Set up Sys2 (Backend-1 + DB Host)

**`.env` on Sys2:**
```env
PORT=5000
FRONTEND_PORT=3000
BACKEND_PORT=5270
AES_GROUP_KEY=<same key as Sys1>
HMAC_SECRET=<same secret as Sys1>
BACKEND_NAME=backend-1
DB_PROXY_PORT=6000
# DB_PATH and UPLOAD_DIR are blank → uses local server/chat.db and server/uploads/
```

**In tmux on Sys2 — open 2 panes:**

```bash
# Pane 1: DB Proxy (MUST start first, before Sys3/Sys4 backends)
cd ~/group-chat-app
python3 server/db_proxy_server.py
# → Listening at https://0.0.0.0:6000

# Pane 2: Backend-1 (normal, uses local SQLite)
cd ~/group-chat-app
python3 server/server.py
# → Listening at https://0.0.0.0:5000 (external: https://10.1.75.51:5270)
```

---

## STEP 4 — Set up Sys3 (Backend-2)

Ask your professor which external port Sys2's internal `6000` is forwarded to — it's **`6270`**.

**`.env` on Sys3:**
```env
PORT=5000
FRONTEND_PORT=3000
BACKEND_PORT=5271
AES_GROUP_KEY=<same key as Sys1>
HMAC_SECRET=<same secret as Sys1>
BACKEND_NAME=backend-2
DB_PROXY_URL=https://10.1.75.51:6270
```

**In tmux on Sys3:**
```bash
cd ~/group-chat-app
bash start_backend.sh
# → Detects DB_PROXY_URL → uses db_client.py automatically
# → Listening at https://0.0.0.0:5000 (external: https://10.1.75.51:5271)
```

---

## STEP 5 — Set up Sys4 (Backend-3)

**`.env` on Sys4:**
```env
PORT=5000
FRONTEND_PORT=3000
BACKEND_PORT=5272
AES_GROUP_KEY=<same key as Sys1>
HMAC_SECRET=<same secret as Sys1>
BACKEND_NAME=backend-3
DB_PROXY_URL=https://10.1.75.51:6270
```

**In tmux on Sys4:**
```bash
cd ~/group-chat-app
bash start_backend.sh
# → Listening at https://0.0.0.0:5000 (external: https://10.1.75.51:5272)
```

---

## STEP 6 — Set up Sys1 (.env)

**`.env` on Sys1:**
```env
PORT=5000
FRONTEND_PORT=3000
BACKEND_PORT=5269       # JS connects to the Load Balancer external port
AES_GROUP_KEY=<your key>
HMAC_SECRET=<your secret>
BACKEND_NAME=lb-node
```

---

## STEP 7 — Run Load Balancer + Frontend on Sys1

**In tmux on Sys1 — open 2 panes:**

```bash
# Pane 1: Load Balancer
cd ~/group-chat-app/load_balancer
./load_balancer \
  -port 5000 \
  -backends "https://10.1.75.51:5270,https://10.1.75.51:5271,https://10.1.75.51:5272" \
  -cert ../cert.pem \
  -key  ../key.pem

# Pane 2: Frontend static server
cd ~/group-chat-app
python3 client/serve.py
# → https://0.0.0.0:3000 (external: https://10.1.75.51:3269)
```

---

## STEP 8 — Verify Everything

```bash
# 1. Check LB is alive
curl -k https://10.1.75.51:5269/lb/health
# → {"status":"ok"}

# 2. Check which backends are healthy
curl -k https://10.1.75.51:5269/lb/status
# → {"backends":[{"url":"...","alive":true,"in_flight":0}, ...]}

# 3. Check metrics
curl -k https://10.1.75.51:5269/lb/metrics

# 4. Check DB proxy
curl -k https://10.1.75.51:6270/health
# → {"status":"ok"}

# 5. Open the app in your browser
# https://10.1.75.51:3269
```

---

## STEP 9 — Run Load Generator Experiments

Run from your **local PC** or from **Sys1** in a separate tmux pane.

### Experiment 1 — Single Backend (bypasses LB, hits Sys2 directly)

```bash
cd ~/group-chat-app/load_generator
./load_generator \
  -url         https://10.1.75.51:5270 \
  -requests    5000 \
  -concurrency 40 \
  -experiment  single_backend \
  -path        /health \
  -out         ./results
```

### Experiment 2 — Three Backends (via Load Balancer)

```bash
./load_generator \
  -url         https://10.1.75.51:5269 \
  -requests    5000 \
  -concurrency 40 \
  -experiment  three_backends \
  -path        /health \
  -out         ./results
```

### Experiment 3 — With Simulated Delay (tests LB timeout)

```bash
./load_generator \
  -url         https://10.1.75.51:5269 \
  -requests    1000 \
  -concurrency 20 \
  -experiment  delay_100ms \
  -path        "/health?delay=100ms" \
  -out         ./results
```

Results are saved to:
- `results/single_backend.json`
- `results/three_backends.json`
- `results/results.csv` ← comparison table for your report

---

## Monitor LB During Experiments

Poll metrics live while the generator is running:

```bash
watch -n 1 'curl -sk https://10.1.75.51:5269/lb/metrics | python3 -m json.tool'
```

---

## tmux Quick Reference

```bash
# Start a new session
tmux new -s chat

# Create a new pane (split horizontally)
Ctrl+B then "

# Switch between panes
Ctrl+B then arrow keys

# Detach (session keeps running)
Ctrl+B then D

# Re-attach
tmux attach -t chat
```

---

## Startup Order (Important!)

Always start in this order:

```
1. Sys2: DB Proxy      → python3 server/db_proxy_server.py
2. Sys2: Backend-1     → python3 server/server.py
3. Sys3: Backend-2     → bash start_backend.sh
4. Sys4: Backend-3     → bash start_backend.sh
5. Sys1: Load Balancer → ./load_balancer/load_balancer -port 5000 ...
6. Sys1: Frontend      → python3 client/serve.py
```

> The DB Proxy **must** be running before Sys3/Sys4 backends start,
> because they connect to it on startup.

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `connection refused` on backend port | Backend not running — check tmux pane |
| LB shows all backends DOWN immediately | Wait ~2s for first health check, then check `/lb/status` |
| `no healthy backends` error from LB | `curl -k https://10.1.75.51:527X/health` to test each backend directly |
| Sys3/Sys4 backend crashes on start | DB Proxy not running yet on Sys2 — start it first |
| `DB_PROXY_URL is not set` error | `DB_PROXY_URL` missing from `.env` on Sys3/Sys4 |
| `certificate verify failed` in curl | Use `curl -k` (skip verify for self-signed cert) |
| LB cert error | Pass `-cert ../cert.pem -key ../key.pem` flags to load_balancer |
| Generator shows 100% dropout | Check LB URL is correct and LB is running |

---

## Port Summary (fill in your actual forwarded ports)

| Service | Machine | Internal | External |
|---------|---------|:---:|:---:|
| Frontend | Sys1 | 3000 | 3269 |
| Load Balancer | Sys1 | 5000 | 5269 |
| Backend-1 | Sys2 | 5000 | 5270 |
| DB Proxy | Sys2 | 6000 | **6270** |
| Backend-2 | Sys3 | 5000 | 5271 |
| Backend-3 | Sys4 | 5000 | 5272 |
