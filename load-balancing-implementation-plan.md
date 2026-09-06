# PixelChat — Dynamic Load Balancing & Bottleneck Fix: Implementation Plan

## 0. Diagnosis (confirmed by reading your code, not just the README)

Your current setup:

```
Sys3 backend --HTTP--> db_proxy_server.py on Sys2 --sqlite3--> chat.db (on Sys2 disk)
Sys4 backend --HTTP--> db_proxy_server.py on Sys2 --sqlite3--> chat.db (on Sys2 disk)
Sys2 backend -------------------------------------> chat.db (local)
```

Every `/rooms`, message save, and history fetch from Sys3/Sys4 is:
`Go LB → Python backend → HTTP call to Sys2 → SQLite write (single-writer lock) → HTTP response back`

Two backends give you **zero** extra write throughput and barely any read throughput, because:
1. SQLite allows exactly one writer at a time (`chat.db` is a single file with a global write lock). Adding backends doesn't add DB capacity — it adds queueing in front of the same lock.
2. You've added an extra network hop (HTTP to Sys2) for backends that aren't the DB host, which makes Sys3/Sys4 *slower per request* than Sys2, not faster in aggregate.
3. The Go LB round-robins blindly across the three, so it keeps sending 1/3 of traffic to two backends that are strictly worse (extra hop) than the third, and none of the three can actually parallelize DB writes.

This is precisely what your professor flagged as "many of you didn't resolve [the DB bottleneck] when you made 3 servers." Fixing this is worth more marks than tuning the LB algorithm — do this first.

---

## 1. Options for the DB layer (explored and compared)

| Option | Write scaling | Read scaling | Effort | Risk to existing features |
|---|---|---|---|---|
| **A. Keep SQLite, just remove the HTTP hop** (sshfs/NFS mount or WAL + shared mount) | None — still 1 writer | Slight | Low | Low, but doesn't fix the real bottleneck |
| **B. PostgreSQL, single primary + read replicas (streaming replication) + PgBouncer** | Real, but still 1 write node | Good (reads spread across replicas) | Medium-high | Medium (new DB engine, new driver, migration) |
| **C. Valkey/Dragonfly as the hot path, primary→replica, SQLite kept for cold data (auth/rooms/XP)** | Very good (in-memory, ~µs writes) | Very good (local replica reads on every backend) | Medium | Low — you only touch the messages hot path |
| **D. "True" active-active (every node accepts writes, e.g. multi-master Postgres/CRDB, or app-level CRDT merge)** | Best in theory | Best | High, easy to get wrong under time pressure | High — conflict resolution, clock skew, dedup gets harder, not needed for write-once/read-many |

**Recommendation: Option C**, for reasons specific to this assignment:

- Your professor explicitly told you the workload is **write-once, read-many** ("every new client is given the full history"). That is *the* textbook case for a **primary–replica** design, not full active-active: writes are rare relative to reads (each new WebSocket join = 1 full history read; each message send = 1 write broadcast to N joined clients). You don't need every node to accept writes — you need every node to serve reads **locally, from memory, with no cross-machine hop**.
- Valkey/Dragonfly give you sub-millisecond ops vs SQLite's disk-locked writes, and both support **replication out of the box** (`REPLICAOF`), which directly gives you "share the same data across 3 machines" without you writing a custom replication protocol.
- It's the smallest change that removes the actual bottleneck: you only replace the **messages hot path** — this means *every* message read/write route (new `/message`+`/feed`, and the existing WebSocket `message`/`history`/`edit_message`/`delete_message` handlers), not just the two new routes. See the explicit table in §2.1. Auth, rooms, XP, uploads, ECDSA/HMAC — all stay exactly as-is in SQLite, satisfying "must not simplify or remove existing functionality."
- Persistence requirement is still met: both Valkey and Dragonfly support AOF/RDB persistence to disk, so a full crash+restart doesn't lose chat history — you're not violating "must use persistent storage."

If you want to go further for extra credit, D (true active-active) is a "nice to have" per the professor's own wording ("just a suggestion, not necessary to follow") — I'd only attempt it after B/C is solid and you have time left, see §7.

---

## 2. Recommended architecture

```
                         ┌──────────────────────────────┐
Browsers/Load-gen ─────► │   Go Load Balancer (Sys1)     │
                         │   dynamic, threshold-based    │
                         └───────────┬───────────────────┘
                     ┌───────────────┼───────────────┐
                     ▼               ▼               ▼
              Backend-1 (Sys2)  Backend-2 (Sys3) Backend-3 (Sys4)
              FastAPI            FastAPI          FastAPI
                 │                  │                 │
           ┌─────┴─────┐      ┌─────┴─────┐     ┌─────┴─────┐
           │ Valkey     │      │ Valkey     │     │ Valkey     │
           │ REPLICA    │◄─────│ REPLICA    │◄────│ REPLICA    │
           │ (local)    │      │ (local)    │     │ (local)    │
           └─────┬──────┘      └────────────┘     └────────────┘
                 │  writes forwarded to primary
                 ▼
           Valkey PRIMARY (co-located on Sys2, AOF persistence on disk)
                 │
     SQLite chat.db (unchanged) — auth, rooms, XP, ECDSA keys, uploads
     lives on whichever machine owns it today; untouched
```

Key points:
- Each backend talks to its **own local Valkey replica** for reads (`/feed`, history-on-join) — no network hop, no shared lock.
- Writes (`/message`, WebSocket `message`) are forwarded by whichever backend received them to the **Valkey primary** (one Redis `SET`/`XADD` command, sub-millisecond, not an HTTP+SQLite round trip). Valkey replicates it to the other two replicas automatically within milliseconds.
- SQLite keeps doing what it's good at: low-frequency, transactional, security-sensitive data (users, rooms, ECDSA keys). This is the part of your app you must not simplify — so don't touch it.

### 2.1 Explicit endpoint-by-endpoint mapping (corrected — read this before implementing)

An earlier version of this plan only called out the two *new* load-gen routes as moving to Valkey. That was incomplete: the existing WebSocket chat path does the same "write once, read many, shared across backends" thing as `/message`/`/feed`, hits the *same* bottleneck today via `db_client.py` → `db_proxy_server.py`, and should move to the same store for the same reason. Leaving it on the old HTTP-proxy-SQLite path means your real chat feature stays bottlenecked even after the assignment's two required routes are fixed. Use this table as the actual scope — every route in your app, one column each:

| Route / handler | File today | Data store | Reasoning |
|---|---|---|---|
| `POST /message` (new) | `server.py` (new) | **Valkey** | Required route, pure hot-path message write |
| `GET /feed` (new) | `server.py` (new) | **Valkey** | Required route, pure hot-path message read |
| WS `message` (send chat message) | `server.py` → `db.save_message` | **Valkey** | Same shape as `/message` — per-message write, currently bottlenecked via `db_proxy_server.py` |
| WS `join` → history-on-connect | `server.py` → `db.get_history` | **Valkey** | Same shape as `/feed` — every new client reads full history; this is literally the "write once, read many" case the professor named |
| WS `edit_message` | `server.py` → `db.edit_message` | **Valkey** | Keyed by `msg_id`, same hash — re-`HSET` the payload in place, keep the 5-min-window check in app code |
| WS `delete_message` | `server.py` → `db.delete_message` | **Valkey** | Soft-delete = set an `is_deleted` flag inside the same JSON payload and re-`HSET`; don't remove the key (keeps dedup history intact) |
| `DELETE /rooms/{id}/history` (creator clears room) | `server.py` → `db.clear_room_history_by_creator` | **Valkey** | Operates on the same per-room hash/sorted-set — `DEL msg:{room}` + `DEL msgorder:{room}` |
| `DELETE /rooms/{id}` (creator deletes room) | `server.py` → `db.delete_room` | **Split**: room row itself → SQLite (unchanged); its message data → Valkey (`DEL` the same two keys) | Room metadata lives in SQLite `rooms` table; its messages live in Valkey — both need cleaning up, in their own stores |
| `POST /register`, `POST /login`, `POST /refresh-token` | `server.py` → `db.create_user`/`get_user`/session tokens | **SQLite (unchanged)** | Low frequency, needs `UNIQUE username` + transactional bcrypt check; not part of the graded load path |
| `GET /rooms`, `POST /rooms`, `GET /rooms/{id}` (metadata only, not history) | `server.py` → `db.create_room`/`get_room`/`list_rooms` | **SQLite (unchanged)** | Rooms are created rarely relative to messages sent; no bottleneck here |
| `GET /users/{name}/xp`, XP awards on send/receive/join/heartbeat | `server.py` → `db.add_xp`/`get_user_xp` | **SQLite (unchanged)**, optional future move to Valkey `HINCRBY` for extra credit | Not required, not graded, SQLite handles this volume fine — don't spend time here |
| `user_keys` (ECDSA public key registry) | `server.py` → `db.register_user_key`/`get_user_key` | **SQLite (unchanged)** | Security-sensitive, once per login/session, low frequency |
| `POST /upload`, `GET /uploads/<file>` | `server.py` | **Unchanged (filesystem, not a DB concern)** | Files live on disk, not in a DB row; if you want uploads shared across backends too, that's a separate NFS/object-storage decision, out of scope here |
| `GET /health`, `GET /config.js`, `GET /lb/health`, `/lb/status`, `/lb/metrics` | `server.py` / Go LB | **N/A** | No persistence involved |

**Net effect:** one Valkey store (per-room hash + sorted-set, from §2's data model) becomes the single place *all* message data lives — new routes and existing chat routes alike. `db.py`/SQLite keeps users, rooms metadata, XP, and keys exactly as they are today. `db_client.py` and `db_proxy_server.py` (the actual bottleneck) get **retired entirely** — once messages move to Valkey, there's no more reason for Sys3/Sys4 to make an HTTP call to Sys2 for anything message-related, and nothing else was ever routed through that proxy.

### Data model in Valkey (per room, or a single "default" room for the load-gen routes)

Use a **Redis Hash** as the source of truth (for O(1) dedup) plus a **Sorted Set** as an ordering index:

```
HSETNX  msg:{room_id}          {msg_id}   {json_payload}     # atomic: returns 0 if msg_id exists → duplicate, drop it
ZADD    msgorder:{room_id} NX  {timestamp} {msg_id}           # NX = don't touch score if already present
```

Run both inside a single Lua script (`EVAL`) so the check-and-insert is atomic across replicas:

```lua
-- dedup_insert.lua
-- KEYS[1] = msg:{room}, KEYS[2] = msgorder:{room}
-- ARGV[1] = msg_id, ARGV[2] = json_payload, ARGV[3] = timestamp
local inserted = redis.call('HSETNX', KEYS[1], ARGV[1], ARGV[2])
if inserted == 1 then
    redis.call('ZADD', KEYS[2], ARGV[3], ARGV[1])
end
return inserted
```

`/feed` becomes: `ZRANGE msgorder:{room} 0 -1` → `HMGET msg:{room} <ids...>` (both O(log n)/O(1), all local reads on any replica).

This is your **duplicate-insertion guarantee**: retries/reconnects sending the same `msg_id` always hit `HSETNX`, which is atomic and idempotent — the second attempt returns `0` and is silently dropped, exactly satisfying the requirement.

### If you'd rather not introduce a new datastore at all (fallback: Option A+)

If time is short, the *minimum* fix that still resolves the bottleneck without adding Valkey — note this still has to cover the same scope as §2.1 (all message read/write paths, not just `/message`/`/feed`), it just does it with SQLite instead:
1. Add `msg_id TEXT UNIQUE` (or a `UNIQUE INDEX`) to `messages` in `db.py`, and change `save_message` to `INSERT ... ON CONFLICT(msg_id) DO NOTHING` — fixes the duplicate-insert requirement regardless of which DB engine you end up using.
2. Put `chat.db` on a genuinely shared filesystem (NFS) instead of the custom HTTP `db_proxy_server.py`, and set `PRAGMA journal_mode=WAL;`. This removes your extra HTTP hop but **does not fix write contention** — WAL improves concurrent readers, not concurrent writers, and NFS + SQLite locking is notoriously flaky under load. I would not present this as your primary fix; use it only as a stopgap if Valkey setup time runs out, and say so honestly in your report (graders will notice if "3 backends" still bottlenecks on one write lock).

---

## 3. Dynamic, performance-based Load Balancer (replacing round-robin)

Your Go LB already has the right skeleton (health loop, atomic in-flight counter, `/lb/status`, `/lb/metrics`) — extend it rather than rewrite it.

### 3.1 Richer health signal from each backend

Add a `/health` payload upgrade in `server.py` (keep the old `{"status":"ok"}` shape working, just add fields — don't break the LB's existing health check):

```python
import psutil, time

@app.get("/health")
async def health_check():
    load1, _, _ = os.getloadavg()
    return {
        "status": "ok",
        "cpu_percent": psutil.cpu_percent(interval=None),
        "load_avg_1m": load1,
        "active_ws_connections": manager.total_connections(),  # you already track this in ConnectionManager
        "valkey_rtt_ms": await probe_valkey_latency(),          # tiny PING timing
    }
```

(`pip install psutil` — light dependency, doesn't touch chat logic.)

### 3.2 Load score + threshold + hysteresis in the LB

In `metrics.go`/`backend.go`, add a `LoadScore` computed from the health payload plus what the LB already knows (in-flight requests, recent latency):

```go
type BackendHealth struct {
    CPUPercent   float64
    LoadAvg1m    float64
    WSConns      int64
    ValkeyRTTms  float64
}

// weighted, normalized 0-100 score — tune weights during experiments
func (b *Backend) computeLoadScore(h BackendHealth) float64 {
    inFlightScore := float64(b.InFlightCount()) * 2.0        // in-flight requests hurt more
    cpuScore      := h.CPUPercent
    connScore     := float64(h.WSConns) * 0.5
    latencyScore  := h.ValkeyRTTms * 3.0
    return inFlightScore + cpuScore + connScore + latencyScore
}
```

Replace binary `alive/dead` with three states, using **hysteresis** (two thresholds, not one) so backends don't flap in and out every health tick:

```go
const (
    ThresholdHigh = 75.0  // above this → mark OVERLOADED, stop sending NEW traffic
    ThresholdLow  = 50.0  // must drop below this to be trusted again
)
```

`healthLoop` updates `b.SetLoadScore(score)` and `b.SetOverloaded(score, ThresholdHigh, ThresholdLow)` (implement hysteresis inside `SetOverloaded` by comparing against the *current* state, not just the raw score).

### 3.3 Selection algorithm (replace `nextBackend()`)

Pure round-robin is explicitly disallowed. Use **weighted power-of-two-choices** among non-overloaded, alive backends — it's simple, avoids the "always pick the single least-loaded" thundering-herd problem, and is dynamic by construction:

```go
func (lb *LoadBalancer) nextBackend() *Backend {
    candidates := lb.aliveAndNotOverloaded()
    if len(candidates) == 0 {
        // graceful degradation: all overloaded — pick least-bad alive backend
        // instead of failing every request
        return lb.leastLoadedAlive()
    }
    if len(candidates) == 1 {
        return candidates[0]
    }
    a := candidates[rand.Intn(len(candidates))]
    b := candidates[rand.Intn(len(candidates))]
    if a.LoadScore() <= b.LoadScore() {
        return a
    }
    return b
}
```

This satisfies both assignment bullets directly:
- **"switch to another suitable backend when threshold exceeded"** → `aliveAndNotOverloaded()` filter + hysteresis.
- **"detect unhealthy/unavailable backends and stop routing"** → your existing `IsAlive()` check, kept as a hard filter before load-score comparison (an unhealthy backend is never a "candidate" regardless of load score).

Also expose the load score in `/lb/status` so your report/screenshots show the LB actually reacting:
```json
{"url":"...5270","alive":true,"overloaded":false,"load_score":32.1,"in_flight":4}
```

---

## 4. Required routes: `/message` and `/feed`

These need to exist **at the Load Balancer's URL**, exact paths, and must not require the E2E-crypto/WebSocket handshake your human users go through (the official load generator will just POST/GET these two routes). Add them as new, separate REST endpoints in `server.py` — don't touch the existing `/ws`, `/register`, `/rooms`, etc.

```python
class SimpleMessageRequest(BaseModel):
    client_name: str = Field(..., alias="client-name")
    msg: str
    msg_id: str | None = None   # allow the caller to supply one for retry-safe dedup

    class Config:
        populate_by_name = True

DEFAULT_FEED_ROOM = "loadtest-feed"   # or reuse "default" — pick one and document it

@app.post("/message")
async def post_message(req: SimpleMessageRequest):
    msg_id = req.msg_id or str(uuid.uuid4())
    ts = time.time()
    inserted = await feed_store.insert_if_new(
        room_id=DEFAULT_FEED_ROOM,
        msg_id=msg_id,
        payload={"client_name": req.client_name, "msg": req.msg, "ts": ts},
    )
    return {"ok": True, "msg_id": msg_id, "duplicate": not inserted}

@app.get("/feed")
async def get_feed():
    return {"messages": await feed_store.get_all(DEFAULT_FEED_ROOM)}
```

`feed_store` is a thin module wrapping the Valkey Lua script from §2. Per §2.1, this is the **same module** the WebSocket `message`/`join`-history/`edit_message`/`delete_message` handlers should call too — not a separate store just for these two routes. Keep it as its own module (not folded into `db.py`) so the encrypted-chat fields (ciphertext, iv, signature, public_key, hmac_digest, sig_valid, reply_to, is_deleted, target_user, is_edited, attachment) travel through unchanged as part of the JSON payload — you're relocating *where* the row lives, not changing *what's in it* or weakening the E2E pipeline. The Go LB needs **no special-casing** for these paths — they flow through the same `serveRequest` reverse-proxy handler as everything else, which is exactly why they need to hit whichever backend the dynamic algorithm currently prefers, and why shared storage (§2/§2.1) matters: a client hitting `/feed` (or joining a room) on whichever backend the LB picks must see messages any backend accepted via `/message` (or WS `message`).

---

## 5. Optimizing the threshold (methodology, not a magic number)

The assignment wants you to *demonstrate* you found a good threshold, not guess one. Procedure:

1. Instrument: LB `/lb/metrics` already gives you dropout%, p50/p95/p99. Add `avg_load_score_by_backend` to the same endpoint.
2. Fix everything except the threshold. Run your own load generator (§6) at a **fixed, moderately high concurrency** (enough to actually push a backend over any reasonable threshold — find this first with a quick ramp test).
3. Sweep `ThresholdHigh` across a small grid, e.g. `{50, 65, 75, 85, 95}` (keep `ThresholdLow = ThresholdHigh - 20` fixed as your hysteresis gap), one full experiment run per value, same load profile each time.
4. For each threshold, record: p95 latency, dropout %, and how evenly requests spread across the 3 backends (`/lb/metrics` counters or your own generator's per-response routing log — you can identify which backend served a request by adding a response header like `X-Backend-Id` in each Python server that the LB/generator can read).
5. Plot **p95 latency vs threshold** and **dropout% vs threshold** on the same x-axis. You're looking for the knee: too low a threshold → you evacuate backends before they're actually struggling (wasted capacity, uneven load); too high → requests queue up and latency/dropout spike before the LB reacts. Pick the threshold at/just before the knee, and say so explicitly in the report with the plot as evidence — that's what "determine an optimal threshold" is actually asking for.

---

## 6. Your own load generator (`load_generator/main.go`)

The spec requires variable users, variable message length, variable inter-message interval, and it must exercise `/message` + `/feed`. Extend the existing Go generator rather than writing a new one:

- **Variable users**: parametrize concurrency as a ramp, not a flat number — e.g. `-min-users 10 -max-users 200 -ramp-step 10 -ramp-interval 5s`, spawning/retiring goroutines over time instead of one fixed worker pool.
- **Variable message length**: `msg := randString(rand.Intn(maxLen-minLen) + minLen)` — sweep e.g. 10–500 chars.
- **Variable interval between messages**: sample from an exponential or uniform distribution per simulated user, e.g. `time.Sleep(time.Duration(rand.ExpFloat64() * float64(meanIntervalMs)) * time.Millisecond)`, so it's not lockstep (which would understate real bursts).
- **System utilization for all 4 systems** (the report explicitly asks for this): have the generator (or a small side script) poll each backend's enhanced `/health` (§3.1) and the LB's `/lb/metrics` on an interval throughout the run, writing CPU%, load avg, in-flight, load-score to a CSV alongside your existing latency CSV — this is what lets you produce the "system utilization of all 4 systems" plots the report requires, correlated against the same timeline as response time.

---

## 7. Optional stretch: closer to "true" active-active

Only attempt after §2–§6 are working and benchmarked. Two lower-risk ways to get closer to the professor's suggestion without full multi-master complexity:

- **Valkey Cluster mode** (sharded, not just replicated): shard rooms across 3 Valkey nodes by `room_id` hash, each backend still has a local replica of *its* shard plus knows how to forward to the right shard's primary for others. More "active" in the sense that all 3 nodes accept writes for their own shard, but avoids full conflict resolution since each room's writes are still owned by one primary.
- **Multi-primary reads with async cross-replication via pub/sub**: each backend keeps a local Valkey instance that both reads and writes locally, and publishes every accepted write to the other two over Valkey Pub/Sub, which apply it via the same idempotent `HSETNX` script from §2. Because inserts are keyed by `msg_id` and idempotent, applying the same write twice (from two "primaries" that both accepted it before syncing) is safe — you just get eventual consistency instead of strict linearizability, which is acceptable for a chat feed. This is genuinely more complex to test correctly (you must verify convergence under concurrent writes to the same room from different backends) — budget real time for it, and don't let it block the core requirements above.

---

## 8. Suggested order of implementation

1. Add `msg_id` UNIQUE constraint + `INSERT OR IGNORE` in `db.py` immediately — cheapest fix, directly satisfies a graded requirement, zero architectural risk.
2. Stand up Valkey primary (Sys2) + 2 replicas (Sys3/Sys4), verify replication with `redis-cli` manually before touching app code.
3. Write the small `feed_store` module (Lua script wrapper) and wire up **all** the routes in the §2.1 table marked "Valkey" — `/message` + `/feed`, and the existing WS `message`/`join`-history/`edit_message`/`delete_message`/room-history-clear handlers — on all 3 backends, against local replica for reads / primary for writes. Retire `db_client.py`/`db_proxy_server.py` once nothing calls them anymore.
4. Confirm via curl/WS client from all 3 machines that: a `/message` POST to any backend shows up in `/feed` on all three, **and** a chat message sent through the real WebSocket UI on one backend shows up in another client's history when it joins via a different backend — this proves the bottleneck is actually fixed for both the required routes and the real app, before you touch the LB.
5. Upgrade `/health` with the metrics payload (§3.1).
6. Implement load-score + hysteresis + weighted-P2C selection in the Go LB (§3.2–3.3).
7. Extend the load generator (§6), run the threshold sweep (§5), pick and document your threshold.
8. Run the full report experiments (single backend vs 3-backend-round-robin-old vs 3-backend-dynamic-new) so your report shows the improvement at each stage, not just the final number.

This order means at every step you have something demonstrably working, and if you run out of time before §7 (active-active), you still have a fully compliant submission satisfying every required bullet in the assignment.
