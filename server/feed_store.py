"""
Valkey-based message store for hot-path chat messages.
With the fan-out write strategy in the load balancer, every backend receives
every POST /message write. This means each backend's local Valkey instance
has a 100% complete copy of all messages — no primary/replica replication needed.
Reads go directly to the local Valkey instance, which is always consistent.
"""

import os
import json
import time
from typing import List, Dict, Optional

import valkey.asyncio as aiovalkey
from dotenv import load_dotenv
from pathlib import Path

load_dotenv(Path(__file__).resolve().parent.parent / ".env")

# Single Valkey connection per backend (reads and writes both go local)
# No primary/replica split needed — fan-out writes guarantee data completeness
_valkey: Optional[aiovalkey.Redis] = None

# Lua script for atomic dedup insert
_LUA_INSERT = """
local inserted = redis.call('HSETNX', KEYS[1], ARGV[1], ARGV[2])
if inserted == 1 then
    redis.call('ZADD', KEYS[2], ARGV[3], ARGV[1])
end
return inserted
"""
_insert_script = None

async def init_feed_store():
    global _valkey, _insert_script

    host = os.environ.get("VALKEY_HOST", "127.0.0.1")
    port = int(os.environ.get("VALKEY_PORT", "4000"))

    _valkey = aiovalkey.Redis(
        host=host, port=port, decode_responses=True,
        max_connections=200,
    )

    _insert_script = _valkey.register_script(_LUA_INSERT)
    print(f"[Valkey] Feed store initialized — {host}:{port} (single local instance, fan-out ensures completeness)")


import asyncio

_batch_queue = []
_batch_task = None
_batch_lock = asyncio.Lock()
BATCH_MAX = 50
BATCH_MS = 0.005

async def _process_batch(batch):
    if not batch:
        return
    try:
        pipe = _valkey.pipeline(transaction=False)
        for room_id, msg_id, payload, _ in batch:
            ts = payload.get("created_at_ts") or time.time()
            await _insert_script(
                keys=[f"msg:{room_id}", f"msgorder:{room_id}"],
                args=[msg_id, json.dumps(payload), float(ts)],
                client=pipe,
            )

        results = await pipe.execute()
        for i, res in enumerate(results):
            if i < len(batch) and not batch[i][3].done():
                batch[i][3].set_result(bool(res))
    except Exception as e:
        for _, _, _, fut in batch:
            if not fut.done():
                fut.set_exception(e)

async def _batch_timer():
    await asyncio.sleep(BATCH_MS)
    global _batch_task
    async with _batch_lock:
        batch = _batch_queue.copy()
        _batch_queue.clear()
        _batch_task = None
    if batch:
        asyncio.create_task(_process_batch(batch))

async def insert_if_new(room_id: str, msg_id: str, payload: dict) -> bool:
    """
    Returns True if inserted, False if it was a duplicate.
    Uses Lua script to atomically HSETNX and ZADD, batched via pipeline.
    """
    if _valkey is None or _insert_script is None:
        raise RuntimeError("Feed store not initialized")
        
    loop = asyncio.get_running_loop()
    fut = loop.create_future()
    
    global _batch_task
    batch_to_process = None
    
    async with _batch_lock:
        _batch_queue.append((room_id, msg_id, payload, fut))
        if len(_batch_queue) >= BATCH_MAX:
            batch_to_process = _batch_queue.copy()
            _batch_queue.clear()
            if _batch_task is not None:
                _batch_task.cancel()
                _batch_task = None
        elif _batch_task is None:
            _batch_task = asyncio.create_task(_batch_timer())
            
    if batch_to_process:
        asyncio.create_task(_process_batch(batch_to_process))
        
    return await fut


async def get_all(room_id: str, limit: int = 0) -> List[Dict]:
    """
    Fetch messages for a room in chronological order.
    limit=0 means all; limit=N returns the latest N messages.
    Reads from the local Valkey instance (always complete due to fan-out writes).
    """
    if _valkey is None:
        raise RuntimeError("Feed store not initialized")

    start_rank = -limit if limit > 0 else 0

    try:
        msg_ids = await _valkey.zrange(f"msgorder:{room_id}", start_rank, -1)
        if not msg_ids:
            return []
        payloads = await _valkey.hmget(f"msg:{room_id}", msg_ids)
    except Exception as e:
        raise RuntimeError(f"Valkey unavailable: {e}")

    messages = []
    for p in payloads:
        if p:
            messages.append(json.loads(p))
    return messages


async def get_msg(room_id: str, msg_id: str) -> Optional[Dict]:
    """
    Fetch a single message.
    """
    if _valkey is None:
        raise RuntimeError("Feed store not initialized")
        
    raw = await _valkey.hget(f"msg:{room_id}", msg_id)
    if raw:
        return json.loads(raw)
    return None


async def soft_delete(room_id: str, msg_id: str) -> bool:
    """
    Soft-delete a message (set is_deleted=True, clear sensitive fields).
    """
    if _valkey is None:
        raise RuntimeError("Feed store not initialized")
        
    key = f"msg:{room_id}"
    raw = await _valkey.hget(key, msg_id)
    if not raw:
        return False
        
    payload = json.loads(raw)
    payload["is_deleted"] = True
    payload["ciphertext"] = ""
    payload["msg"] = ""
    payload["iv"] = ""
    payload["signature"] = ""
    
    await _valkey.hset(key, msg_id, json.dumps(payload))
    return True


async def edit_msg(room_id: str, msg_id: str, new_ciphertext: str, new_iv: str, new_sig: str, new_hmac: str, sig_valid: bool) -> bool:
    """
    Update an existing message.
    """
    if _valkey is None:
        raise RuntimeError("Feed store not initialized")
        
    key = f"msg:{room_id}"
    raw = await _valkey.hget(key, msg_id)
    if not raw:
        return False
        
    payload = json.loads(raw)
    payload["ciphertext"] = new_ciphertext
    payload["msg"] = new_ciphertext  # keep both fields in sync
    payload["iv"] = new_iv
    payload["signature"] = new_sig
    payload["hmac_digest"] = new_hmac
    payload["sig_valid"] = sig_valid
    payload["is_edited"] = True
    
    await _valkey.hset(key, msg_id, json.dumps(payload))
    return True


async def delete_room_messages(room_id: str) -> None:
    """
    Permanently delete all messages for a room.
    """
    if _valkey is None:
        raise RuntimeError("Feed store not initialized")
        
    await _valkey.delete(f"msg:{room_id}", f"msgorder:{room_id}")


async def probe_latency() -> float:
    """
    Ping the local Valkey instance and return latency in ms.
    Returns -1.0 if Valkey is unreachable.
    """
    start = time.perf_counter()
    try:
        if _valkey is not None:
            await _valkey.ping()
            return (time.perf_counter() - start) * 1000.0
    except Exception:
        pass
    return -1.0
