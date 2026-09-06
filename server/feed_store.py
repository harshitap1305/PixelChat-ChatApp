"""
Valkey-based message store for hot-path chat messages.
Uses primary for writes and local replica for reads.
"""

import os
import json
import time
from typing import List, Dict, Optional

import valkey.asyncio as aiovalkey
from dotenv import load_dotenv
from pathlib import Path

load_dotenv(Path(__file__).resolve().parent.parent / ".env")

# Global connections
_primary: Optional[aiovalkey.Redis] = None
_replica: Optional[aiovalkey.Redis] = None

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
    global _primary, _replica, _insert_script
    
    primary_host = os.environ.get("VALKEY_HOST", "127.0.0.1")
    primary_port = int(os.environ.get("VALKEY_PORT", "6379"))
    
    replica_host = os.environ.get("VALKEY_REPLICA_HOST", "127.0.0.1")
    replica_port = int(os.environ.get("VALKEY_REPLICA_PORT", "6379"))
    
    _primary = aiovalkey.Redis(host=primary_host, port=primary_port, decode_responses=True)
    _replica = aiovalkey.Redis(host=replica_host, port=replica_port, decode_responses=True)
    
    _insert_script = _primary.register_script(_LUA_INSERT)
    print(f"[Valkey] Feed store initialized. Primary: {primary_host}:{primary_port}, Replica: {replica_host}:{replica_port}")


async def insert_if_new(room_id: str, msg_id: str, payload: dict) -> bool:
    """
    Returns True if inserted, False if it was a duplicate.
    Uses Lua script to atomically HSETNX and ZADD.
    """
    if _primary is None or _insert_script is None:
        raise RuntimeError("Feed store not initialized")
        
    ts = payload.get("created_at_ts")
    if not ts:
        ts = time.time()
        
    inserted = await _insert_script(
        keys=[f"msg:{room_id}", f"msgorder:{room_id}"],
        args=[msg_id, json.dumps(payload), float(ts)]
    )
    return bool(inserted)


async def get_all(room_id: str) -> List[Dict]:
    """
    Fetch all messages for a room, in chronological order.
    Reads from the local replica.
    """
    if _replica is None:
        raise RuntimeError("Feed store not initialized")
        
    msg_ids = await _replica.zrange(f"msgorder:{room_id}", 0, -1)
    if not msg_ids:
        return []
        
    payloads = await _replica.hmget(f"msg:{room_id}", msg_ids)
    
    messages = []
    for p in payloads:
        if p:
            messages.append(json.loads(p))
    return messages


async def get_msg(room_id: str, msg_id: str) -> Optional[Dict]:
    """
    Fetch a single message.
    """
    if _replica is None:
        raise RuntimeError("Feed store not initialized")
        
    raw = await _replica.hget(f"msg:{room_id}", msg_id)
    if raw:
        return json.loads(raw)
    return None


async def soft_delete(room_id: str, msg_id: str) -> bool:
    """
    Soft-delete a message (set is_deleted=True, clear sensitive fields).
    """
    if _primary is None:
        raise RuntimeError("Feed store not initialized")
        
    key = f"msg:{room_id}"
    raw = await _primary.hget(key, msg_id)
    if not raw:
        return False
        
    payload = json.loads(raw)
    payload["is_deleted"] = True
    payload["ciphertext"] = ""
    payload["iv"] = ""
    payload["signature"] = ""
    
    await _primary.hset(key, msg_id, json.dumps(payload))
    return True


async def edit_msg(room_id: str, msg_id: str, new_ciphertext: str, new_iv: str, new_sig: str, new_hmac: str, sig_valid: bool) -> bool:
    """
    Update an existing message.
    """
    if _primary is None:
        raise RuntimeError("Feed store not initialized")
        
    key = f"msg:{room_id}"
    raw = await _primary.hget(key, msg_id)
    if not raw:
        return False
        
    payload = json.loads(raw)
    payload["ciphertext"] = new_ciphertext
    payload["iv"] = new_iv
    payload["signature"] = new_sig
    payload["hmac_digest"] = new_hmac
    payload["sig_valid"] = sig_valid
    payload["is_edited"] = True
    
    await _primary.hset(key, msg_id, json.dumps(payload))
    return True


async def delete_room_messages(room_id: str) -> None:
    """
    Permanently delete all messages for a room.
    """
    if _primary is None:
        raise RuntimeError("Feed store not initialized")
        
    await _primary.delete(f"msg:{room_id}", f"msgorder:{room_id}")


async def probe_latency() -> float:
    """
    Ping the primary and return latency in ms.
    """
    if _primary is None:
        return 0.0
        
    start = time.perf_counter()
    try:
        await _primary.ping()
        return (time.perf_counter() - start) * 1000.0
    except Exception:
        return -1.0
