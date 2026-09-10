"""
DB Client — db_client.py
=========================
Drop-in replacement for db.py on Sys3 and Sys4.
Has EXACTLY the same function signatures as db.py but calls the
DB Proxy Server running on Sys2 over HTTPS instead of SQLite directly.

HOW TO USE (on Sys3 and Sys4):
  1. Set DB_PROXY_URL in .env, e.g.:
       DB_PROXY_URL=https://10.1.75.51:<forwarded_port_for_sys2_db_proxy>
  2. The startup script automatically uses this file when DB_PROXY_URL is set.
     No changes needed to server.py — it still does  `import db`  and gets
     this module transparently via the PYTHONPATH trick in start_backend.sh.

IMPORTANT: Do NOT set both DB_PROXY_URL and DB_PATH at the same time.
"""

import os
import json
import sqlite3  # only for IntegrityError re-raising
from typing import Optional

import requests
from dotenv import load_dotenv
from pathlib import Path

load_dotenv(Path(__file__).resolve().parent.parent / ".env")

_PROXY_URL = os.environ.get("DB_PROXY_URL", "").rstrip("/")
if not _PROXY_URL:
    raise RuntimeError(
        "db_client.py loaded but DB_PROXY_URL is not set in .env.\n"
        "Either set DB_PROXY_URL=https://10.1.75.51:<port> or use the "
        "original db.py (don't set DB_PROXY_URL on Sys2)."
    )

# Skip TLS verification for self-signed lab certificates
_SESSION = requests.Session()
_SESSION.verify = False

# Suppress the InsecureRequestWarning noise in logs
import urllib3
urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)


def _post(path: str, body: dict):
    """POST JSON to the proxy, return parsed response dict."""
    resp = _SESSION.post(f"{_PROXY_URL}{path}", json=body, timeout=10)
    resp.raise_for_status()
    return resp.json()


def _get(path: str):
    """GET from the proxy, return parsed response dict."""
    resp = _SESSION.get(f"{_PROXY_URL}{path}", timeout=10)
    resp.raise_for_status()
    return resp.json()


# ── Init (no-op on clients — proxy server handles this) ──────────────────────

def init_db() -> None:
    """No-op: DB is initialised by the proxy server on Sys2."""
    print(f"[DB Client] Using remote DB proxy at {_PROXY_URL}")


# ── Rooms ─────────────────────────────────────────────────────────────────────

def create_room(room_id: str, name: str, created_by: str,
                is_public: bool = True, avatar: str = "🏰") -> None:
    _post("/db/create_room", {
        "room_id": room_id, "name": name, "created_by": created_by,
        "is_public": is_public, "avatar": avatar,
    })


def get_room(room_id: str) -> dict | None:
    return _get(f"/db/get_room/{room_id}")["result"]


def update_room_creator_if_system(room_id: str, username: str) -> None:
    _post("/db/update_room_creator", {"room_id": room_id, "username": username})


def list_rooms() -> list[dict]:
    return _get("/db/list_rooms")["result"]


def delete_room(room_id: str, username: str) -> bool:
    return _post("/db/delete_room", {"room_id": room_id, "username": username})["result"]


def clear_room_history_by_creator(room_id: str, username: str) -> bool:
    return _post("/db/clear_room_history_by_creator",
                 {"room_id": room_id, "username": username})["result"]


def clear_room_history(room_id: str) -> None:
    # Called internally — proxy handles via delete_message cascade
    _post("/db/clear_room_history_by_creator", {"room_id": room_id, "username": ""})


# ── Messages ──────────────────────────────────────────────────────────────────

def save_message(
    room_id: str,
    username: str,
    avatar: str,
    ciphertext: str,
    iv: str,
    signature: str,
    public_key: dict,
    timestamp: str,
    sig_valid: bool,
    msg_id: str = "",
    reply_to: str | None = None,
    target_user: str | None = None,
    attachment: str | None = None,
) -> int:
    return _post("/db/save_message", {
        "room_id": room_id, "username": username, "avatar": avatar,
        "ciphertext": ciphertext, "iv": iv, "signature": signature,
        "public_key": public_key, "timestamp": timestamp,
        "sig_valid": sig_valid, "msg_id": msg_id,
        "reply_to": reply_to, "target_user": target_user,
        "attachment": attachment,
    })["result"]


def get_history(room_id: str, limit: int | None = None,
                username: str | None = None) -> list[dict]:
    return _post("/db/get_history", {
        "room_id": room_id, "limit": limit, "username": username,
    })["result"]


def get_message_by_id(msg_id: str) -> dict | None:
    return _get(f"/db/get_message_by_id/{msg_id}")["result"]


def delete_message(msg_id: str, username: str) -> bool:
    return _post("/db/delete_message",
                 {"msg_id": msg_id, "username": username})["result"]


def edit_message(
    msg_id: str, username: str,
    ciphertext: str, iv: str, signature: str, sig_valid: bool,
) -> tuple[bool, str]:
    resp = _post("/db/edit_message", {
        "msg_id": msg_id, "username": username,
        "ciphertext": ciphertext, "iv": iv,
        "signature": signature, "sig_valid": sig_valid,
    })
    return resp["result"], resp["error"]


# ── User keys ─────────────────────────────────────────────────────────────────

def register_user_key(username: str, public_key: dict) -> None:
    _post("/db/register_user_key", {"username": username, "public_key": public_key})


def get_user_key(username: str) -> dict | None:
    return _get(f"/db/get_user_key/{username}")["result"]


# ── Users ─────────────────────────────────────────────────────────────────────

def create_user(username: str, password_hash: str, avatar: str) -> None:
    resp = _SESSION.post(f"{_PROXY_URL}/db/create_user", json={
        "username": username, "password_hash": password_hash, "avatar": avatar,
    }, timeout=10)
    if resp.status_code == 409:
        raise sqlite3.IntegrityError(f"UNIQUE constraint failed: username '{username}'")
    resp.raise_for_status()


def get_user(username: str) -> dict | None:
    return _get(f"/db/get_user/{username}")["result"]


def add_xp(username: str, amount: int) -> int:
    return _post("/db/add_xp", {"username": username, "amount": amount})["result"]


def get_user_xp(username: str) -> int:
    return _get(f"/db/get_user_xp/{username}")["result"]


# ── Session Tokens ────────────────────────────────────────────────────────────

def save_session_token(token: str, username: str, avatar: str) -> None:
    _post("/db/save_session_token", {"token": token, "username": username, "avatar": avatar})

def consume_session_token(token: str) -> dict | None:
    return _get(f"/db/consume_session_token/{token}")["result"]


# ── Legacy / unused on clients ────────────────────────────────────────────────


def clear_history() -> None:
    """No-op on clients — only Sys2 can clear all history."""
    pass
