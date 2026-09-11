# Load Balancer & Valkey — Deployment Guide

## System Overview

| Machine | Role | Internal Port | External Port | Notes |
|---------|------|:---:|:---:|------|
| **Sys1** | Frontend + Go Load Balancer | `3000` / `5000` | `3269` / `5269` | Your machine |
| **Sys2** | Backend-1 + DB Proxy + Valkey Primary | `5000` / `6000` / `4000` | `5270` / `6270` / `4270` | Hosts `chat.db` and Valkey Primary |
| **Sys3** | Backend-2 + Valkey Replica | `5000` / `4000` | `5271` / `4271` | Points to Sys2 DB and Valkey Primary |
| **Sys4** | Backend-3 + Valkey Replica | `5000` / `4000` | `5272` / `4272` | Points to Sys2 DB and Valkey Primary |
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
  Valkey Pri     Valkey Rep│ Valkey Rep   ← (Message hot-path, all internal :4000)
    (Sys2)         (Sys3)  │   (Sys4)
                           │
  DB Proxy                 │
  (Sys2 :6000)             │
  local chat.db ◄──────────┴───────────── ← (Auth / Rooms)
```

---

## Prerequisites (all machines)

```bash
# System dependencies (for the Valkey/Redis binary)
sudo apt update && sudo apt install redis-server -y

# Python deps
pip install -r server/requirements.txt
pip install valkey psutil

# Check Git is up to date on all machines
git pull origin main
```

> ⚠️ **Run `git pull` on ALL machines before starting anything.**
> Scripts like `valkey_primary.sh` and `valkey_replica.sh` have been updated
> and old versions will not work correctly.

---

## Performance Optimizations Applied (For Load Testing)

To achieve maximum throughput during load testing, the following optimizations have been applied to this architecture:
1. **Load Generator Connection Pooling:** Fixed the HTTP client to fully read response bodies, enabling connection reuse.
2. **Load Balancer Thundering Herd Fix:** The Load Balancer computes the `isOverloaded` state directly from atomic `in_flight` counters instead of lagging health checks.
3. **Valkey AOF Disabled:** `valkey_primary.sh` runs with `--appendonly no` to disable disk writes, unlocking maximum in-memory throughput.
4. **Valkey Connection Pool:** `feed_store.py` uses an explicit connection pool of 200 to prevent queuing delays.

---

## STEP 0 — Generate TLS Certificates (all machines)

Run on **every machine** (Sys1, Sys2, Sys3, Sys4):

```bash
cd ~/PixelChat-ChatApp
python3 generate_certs.py
# → Generated cert.pem and key.pem
```

> **Important:** The certificate now includes `10.1.75.51` in its Subject Alternative
> Names (SANs). This is required for modern browsers — if the IP is not in the SAN
> list the browser will always reject the connection even if you click "Proceed".

After starting ALL servers, you must **accept the cert in the browser once per URL**:

1. Open each of these in a new tab and click **Advanced → Proceed (unsafe)**:
   - `https://10.1.75.51:5269` (Load Balancer)
   - `https://10.1.75.51:5270` (Backend-1)
   - `https://10.1.75.51:5271` (Backend-2)
   - `https://10.1.75.51:5272` (Backend-3)
   - `https://10.1.75.51:6270` (DB Proxy)
2. Then open the app at `https://10.1.75.51:3269` and do a **hard refresh** (`Ctrl+Shift+R`).

> This step is **not optional** — without it the browser silently refuses all WebSocket
> (`wss://`) connections, causing a reconnect loop and TLS error spam in the LB logs.

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
VALKEY_PORT=4000
VALKEY_REPLICA_HOST=127.0.0.1
VALKEY_REPLICA_PORT=4000
# DB_PATH and UPLOAD_DIR are blank → uses local server/chat.db and server/uploads/
```

**In tmux on Sys2 — open 3 panes:**

```bash
# Pane 1: Valkey Primary (Internal Port 4000)
cd ~/PixelChat-ChatApp
bash scripts/valkey_primary.sh 4000
# → Listening at 0.0.0.0:4000

# Pane 2: DB Proxy (Internal Port 6000, MUST start before Sys3/Sys4 backends)
cd ~/PixelChat-ChatApp
python3 server/db_proxy_server.py
# → Listening at https://0.0.0.0:6000 (external: https://10.1.75.51:6270)

# Pane 3: Backend-1 (Internal Port 5000, normal)
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
VALKEY_HOST=10.1.75.51
VALKEY_PORT=4270
VALKEY_REPLICA_HOST=127.0.0.1
VALKEY_REPLICA_PORT=4000
```

**In tmux on Sys3 — open 2 panes:**
```bash
# Pane 1: Valkey Replica (Internal Port 4000)
cd ~/PixelChat-ChatApp
bash scripts/valkey_replica.sh 10.1.75.51 4270 4000

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
VALKEY_HOST=10.1.75.51
VALKEY_PORT=4270
VALKEY_REPLICA_HOST=127.0.0.1
VALKEY_REPLICA_PORT=4000
```

**In tmux on Sys4 — open 2 panes:**
```bash
# Pane 1: Valkey Replica (Internal Port 4000)
cd ~/PixelChat-ChatApp
bash scripts/valkey_replica.sh 10.1.75.51 4270 4000

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
# Pane 1: Load Balancer (HTTP Mode for Leaderboard, forwarding to HTTPS Backends)
cd ~/PixelChat-ChatApp/load_balancer
pkill -f load_balancer
go build -o load_balancer main.go
./load_balancer \
  -port 5000 \
  -backends "https://10.1.75.51:5270,https://10.1.75.51:5271,https://10.1.75.51:5272"
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
**Note:** We use the new `-mode` flag to test read vs write performance and generate utilization plots.

### Experiment 1 — Single Backend: Write-only

```bash
cd PixelChat-ChatApp/load_generator
./load_generator -url https://10.1.75.51:5271 -requests 20000 -concurrency 200 \
  -experiment single_write -mode message -users 100 -min-len 20 -max-len 300 \
  -health-urls https://10.1.75.51:5271/health -out ./results
```

### Experiment 2 — Single Backend: Read-only

```bash
./load_generator -url https://10.1.75.51:5271 -requests 20000 -concurrency 200 \
  -experiment single_read -mode feed \
  -health-urls https://10.1.75.51:5271/health -out ./results
```

### Experiment 3 — Single Backend: Mixed (80% Writes / 20% Reads)

```bash
./load_generator -url https://10.1.75.51:5271 -requests 20000 -concurrency 200 \
  -experiment single_mixed -mode mixed -read-ratio 0.2 -users 100 -min-len 20 -max-len 300 \
  -health-urls https://10.1.75.51:5271/health -out ./results
```

### Experiment 4 — Three Backends (LB): Write-only

```bash
./load_generator -url https://10.1.75.51:5269 -requests 20000 -concurrency 200 \
  -experiment lb_write -mode message -users 100 -min-len 20 -max-len 300 \
  -health-urls https://10.1.75.51:5270/health,https://10.1.75.51:5271/health,https://10.1.75.51:5272/health -out ./results
```

### Experiment 5 — Three Backends (LB): Read-only

```bash
./load_generator -url https://10.1.75.51:5269 -requests 20000 -concurrency 200 \
  -experiment lb_read -mode feed \
  -health-urls https://10.1.75.51:5270/health,https://10.1.75.51:5271/health,https://10.1.75.51:5272/health -out ./results
```

### Experiment 6 — Three Backends (LB): Mixed (80% Writes / 20% Reads)

```bash
./load_generator -url https://10.1.75.51:5269 -requests 20000 -concurrency 200 \
  -experiment lb_mixed -mode mixed -read-ratio 0.2 -users 100 -min-len 20 -max-len 300 \
  -health-urls https://10.1.75.51:5270/health,https://10.1.75.51:5271/health,https://10.1.75.51:5272/health \
  -out ./results
```

### STEP 10 — Generate Report Plots

Once all experiments have run, generate the assignment plots:

```bash
# Install dependencies if needed
pip install matplotlib pandas numpy

# Generate 10 plots into the plots/ directory
python3 plot_results.py --results results/results.csv --utilization results/utilization.csv --out plots/
```

Results are saved to:
- `results/*.json`
- `results/results.csv` ← cumulative comparison table
- `results/utilization.csv` ← per-system CPU/Valkey metrics
- `plots/*.png` ← The charts for your report!

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
1. Sys2: Valkey Primary  → bash scripts/valkey_primary.sh 4000
2. Sys3: Valkey Replica  → bash scripts/valkey_replica.sh 10.1.75.51 4270 4000
3. Sys4: Valkey Replica  → bash scripts/valkey_replica.sh 10.1.75.51 4270 4000
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
| `Valkey ConnectionError` | Make sure Valkey primary is running on Sys2. Check `VALKEY_HOST` and `VALKEY_PORT` in `.env` |
| `DB_PROXY_URL is not set` error | `DB_PROXY_URL` missing from `.env` on Sys3/Sys4 |
| `certificate verify failed` in curl | Use `curl -k` (skip verify for self-signed cert) |
| LB cert error | Pass `-cert ../cert.pem -key ../key.pem` flags to load_balancer |
| Generator shows 100% dropout | Check LB URL is correct and LB is running |

---

## Port Summary

| Service | Machine | Internal | External |
|---------|---------|:---:|:---:|
| Frontend | Sys1 | 3000 | 3269 |
| Load Balancer | Sys1 | 5000 | 5269 |
| Backend-1 | Sys2 | 5000 | 5270 |
| DB Proxy | Sys2 | 6000 | 6270 |
| Valkey Primary| Sys2 | 4000 | 4270 |
| Backend-2 | Sys3 | 5000 | 5271 |
| Valkey Replica| Sys3 | 4000 | 4271 |
| Backend-3 | Sys4 | 5000 | 5272 |
| Valkey Replica| Sys4 | 4000 | 4272 |


---

## How to Completely Wipe Data (For a Fresh Start)

Run these exact commands to completely wipe all chat history, databases, and memory so you have a 100% fresh start before a load test.

### 1. On Sys2 (Primary Database Server)
Run these commands in the terminal to wipe the SQLite database, flush the Valkey memory, and delete its persistent storage files:
```bash
# 1. Delete the SQLite database file
rm -f ~/PixelChat-ChatApp/server/chat.db

# 2. Flush Valkey memory (if it's currently running)
redis-cli -p 4000 flushall

# 3. Stop Valkey and delete its persistent AOF/RDB files
pkill -f redis-server
rm -f ~/PixelChat-ChatApp/*.aof
rm -f ~/PixelChat-ChatApp/*.rdb
```
*(After this, you can restart Valkey Primary with `bash scripts/valkey_primary.sh 4000` and restart your backend).*

### 2. On Sys3 and Sys4 (Replica Servers)
Run these commands to stop the replicas and clear any leftover storage files:
```bash
# 1. Stop the replica
pkill -f redis-server

# 2. Delete any synced storage files
rm -f ~/PixelChat-ChatApp/*.rdb
rm -f ~/PixelChat-ChatApp/*.aof
```
*(After this, you can restart your replicas using `bash scripts/valkey_replica.sh 10.1.75.51 4270 4000` and restart your backends).*

Once you've done this across your servers, your app will be completely empty (0 users, 0 messages) and perfectly clean for your final load tests!
