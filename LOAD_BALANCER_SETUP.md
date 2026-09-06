# Load Balancer & Valkey — Deployment Guide

## System Overview

| Machine | Role | Internal Port | External Port | Notes |
|---------|------|:---:|:---:|------|
| **Sys1** | Frontend + Go Load Balancer | `3000` / `5000` | `3269` / `5269` | Your machine |
| **Sys2** | Backend-1 + **DB Host** + DB Proxy + **Valkey Primary** | `5000` / `6000` / `6379` | `5270` / `6270` / `6379` | Hosts `chat.db` and Valkey Primary |
| **Sys3** | Backend-2 + **Valkey Replica** | `5000` / `6379` | `5271` / `6379` | Points to Sys2 DB and Valkey Primary |
| **Sys4** | Backend-3 + **Valkey Replica** | `5000` / `6379` | `5272` / `6379` | Points to Sys2 DB and Valkey Primary |
| **Local PC** | Load Generator | — | — | Sends load to LB |

**Shared IP:** `10.1.75.51`

> **Storage Separation**: 
> - **SQLite** (via DB Proxy on Sys2): Used for auth, users, rooms, and XP.
> - **Valkey** (In-Memory on Sys2/3/4): Used for the high-volume chat messages. Sys2 runs the primary (for writes), while Sys3 and Sys4 run replicas (for local reads).

---

## Architecture

```
Browser → https://10.1.75.51:3269
              │
              │  (Frontend JS uses BACKEND_PORT=5269)
              ▼
    ┌─────────────────────────┐
    │   Go Load Balancer      │  ← Sys1, internal :5000, external :5269
    │   P2C + Health Routing  │
    └──────┬──────────┬───────┘
           │          │          │
           ▼          ▼          ▼
      :5270        :5271       :5272
    Backend-1    Backend-2   Backend-3
    (Sys2)       (Sys3)      (Sys4)
       │             │           │
       │             ├─────┬─────┤
       ▼             ▼     │     ▼ 
  Valkey Pri     Valkey Rep│ Valkey Rep   ← (Message hot-path)
    (Sys2)         (Sys3)  │   (Sys4)
                           │
  DB Proxy                 │
  local chat.db ◄──────────┴───────────── ← (Auth / Rooms)
```

---

## Prerequisites (all machines)

```bash
# Python deps
pip install -r server/requirements.txt
pip install valkey psutil

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
cd ~/PixelChat-ChatApp

# Build Load Balancer
cd load_balancer && go build -o load_balancer . && cd ..

# Build Load Generator (used for experiments)
cd load_generator && go build -o load_generator . && cd ..

echo "✅ Binaries ready"
```

---

## STEP 3 — Set up Sys2 (Backend-1 + DB Host + Valkey Primary)

**`.env` on Sys2:**
```env
PORT=5000
FRONTEND_PORT=3000
BACKEND_PORT=5270
AES_GROUP_KEY=<same key as Sys1>
HMAC_SECRET=<same secret as Sys1>
BACKEND_NAME=backend-1
DB_PROXY_PORT=6000
VALKEY_HOST=127.0.0.1
VALKEY_REPLICA_HOST=127.0.0.1
# DB_PATH and UPLOAD_DIR are blank → uses local server/chat.db and server/uploads/
```

**In tmux on Sys2 — open 3 panes:**

```bash
# Pane 1: Valkey Primary
cd ~/PixelChat-ChatApp
bash scripts/valkey_primary.sh
# → Listening at 0.0.0.0:6379

# Pane 2: DB Proxy (MUST start before Sys3/Sys4 backends)
cd ~/PixelChat-ChatApp
python3 server/db_proxy_server.py
# → Listening at https://0.0.0.0:6000

# Pane 3: Backend-1 (normal, uses local SQLite & local Valkey)
cd ~/PixelChat-ChatApp
python3 server/server.py
# → Listening at https://0.0.0.0:5000 (external: https://10.1.75.51:5270)
```

---

## STEP 4 — Set up Sys3 (Backend-2 + Valkey Replica)

**`.env` on Sys3:**
```env
PORT=5000
FRONTEND_PORT=3000
BACKEND_PORT=5271
AES_GROUP_KEY=<same key as Sys1>
HMAC_SECRET=<same secret as Sys1>
BACKEND_NAME=backend-2
DB_PROXY_URL=https://10.1.75.51:6270
VALKEY_HOST=10.1.75.51  # Sys2's IP
VALKEY_REPLICA_HOST=127.0.0.1
```

**In tmux on Sys3 — open 2 panes:**
```bash
# Pane 1: Valkey Replica
cd ~/PixelChat-ChatApp
bash scripts/valkey_replica.sh 10.1.75.51

# Pane 2: Backend-2
cd ~/PixelChat-ChatApp
bash start_backend.sh
# → Detects DB_PROXY_URL → uses db_client.py automatically
# → Connects to local Valkey Replica for reads
# → Listening at https://0.0.0.0:5000 (external: https://10.1.75.51:5271)
```

---

## STEP 5 — Set up Sys4 (Backend-3 + Valkey Replica)

**`.env` on Sys4:**
```env
PORT=5000
FRONTEND_PORT=3000
BACKEND_PORT=5272
AES_GROUP_KEY=<same key as Sys1>
HMAC_SECRET=<same secret as Sys1>
BACKEND_NAME=backend-3
DB_PROXY_URL=https://10.1.75.51:6270
VALKEY_HOST=10.1.75.51  # Sys2's IP
VALKEY_REPLICA_HOST=127.0.0.1
```

**In tmux on Sys4 — open 2 panes:**
```bash
# Pane 1: Valkey Replica
cd ~/PixelChat-ChatApp
bash scripts/valkey_replica.sh 10.1.75.51

# Pane 2: Backend-3
cd ~/PixelChat-ChatApp
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
cd ~/PixelChat-ChatApp/load_balancer
./load_balancer \
  -port 5000 \
  -backends "https://10.1.75.51:5270,https://10.1.75.51:5271,https://10.1.75.51:5272" \
  -cert ../cert.pem \
  -key  ../key.pem

# Pane 2: Frontend static server
cd ~/PixelChat-ChatApp
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
# → {"backends":[{"url":"...","alive":true,"in_flight":0,"load_score":...,"overloaded":...}, ...]}

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
**Note:** We use the new `-mode message` flag to spam the `/message` endpoint directly via POST requests.

### Experiment 1 — Single Backend (bypasses LB, hits Sys3 directly)

```bash
cd ~/PixelChat-ChatApp/load_generator
./load_generator \
  -url         https://10.1.75.51:5271 \
  -requests    5000 \
  -concurrency 50 \
  -experiment  single_backend \
  -path        /message \
  -mode        message \
  -out         ./results
```

### Experiment 2 — Three Backends (via Load Balancer)

```bash
./load_generator \
  -url         https://10.1.75.51:5269 \
  -requests    15000 \
  -concurrency 150 \
  -experiment  three_backends \
  -path        /message \
  -mode        message \
  -poll-lb \
  -out         ./results
```

### Experiment 3 — Stress Test with Heavy Load

```bash
./load_generator \
  -url         https://10.1.75.51:5269 \
  -requests    50000 \
  -concurrency 500 \
  -experiment  stress_50k \
  -path        /message \
  -mode        message \
  -poll-lb \
  -out         ./results
```

Results are saved to:
- `results/single_backend.json`
- `results/three_backends.json`
- `results/stress_50k.json`
- `results/results.csv` ← cumulative comparison table for your report

---

## Monitor LB During Experiments

The `-poll-lb` flag in the generator automatically queries `/lb/status` every 2 seconds, but you can also poll metrics live in another terminal window while the generator is running:

```bash
watch -n 1 'curl -sk https://10.1.75.51:5269/lb/metrics | python3 -m json.tool'
```

---

## Startup Order (Important!)

Always start in this exact order to ensure successful connections:

```
1. Sys2: Valkey Primary  → bash scripts/valkey_primary.sh
2. Sys3: Valkey Replica  → bash scripts/valkey_replica.sh <Sys2_IP>
3. Sys4: Valkey Replica  → bash scripts/valkey_replica.sh <Sys2_IP>
4. Sys2: DB Proxy        → python3 server/db_proxy_server.py
5. Sys2: Backend-1       → python3 server/server.py
6. Sys3: Backend-2       → bash start_backend.sh
7. Sys4: Backend-3       → bash start_backend.sh
8. Sys1: Load Balancer   → ./load_balancer -port 5000 ...
9. Sys1: Frontend        → python3 client/serve.py
```

> **Why this order?**
> - Valkey Replicas must connect to the Primary on boot.
> - The DB Proxy **must** be running before Sys3/Sys4 backends start.
> - The LB should only start once the backends are ready to serve `/health` checks.

---

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `connection refused` on backend port | Backend not running — check tmux pane |
| LB shows all backends DOWN immediately | Wait ~2s for first health check, then check `/lb/status` |
| `no healthy backends` error from LB | `curl -k https://10.1.75.51:527X/health` to test each backend directly |
| Sys3/Sys4 backend crashes on start | DB Proxy not running yet on Sys2 — start it first |
| `Valkey ConnectionError` | Make sure Valkey primary is running on Sys2 on port 6379, and replicas are running on Sys3/4. Check `VALKEY_HOST` in `.env` |
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
| Valkey Primary| Sys2 | 6379 | **6379** |
| Backend-2 | Sys3 | 5000 | 5271 |
| Valkey Replica| Sys3 | 6379 | 6379 |
| Backend-3 | Sys4 | 5000 | 5272 |
| Valkey Replica| Sys4 | 6379 | 6379 |
