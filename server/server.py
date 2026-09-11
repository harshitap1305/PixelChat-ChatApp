"""
Secure Persistent Group Chat — Server
======================================
Extends the original WebSocket group chat with:
  - SQLite-backed message persistence (encrypted at rest)
  - AES-GCM symmetric encryption (key served from .env via /group-key)
  - Per-user ECDSA-P256 signing key pairs (server verifies every message)
  - HMAC-SHA256 database tamper detection
  - Multi-room support: rooms identified by unique 6-char codes
"""

import json
import asyncio
import os
import uuid
import shutil
import base64
import hmac
import hashlib
import secrets
import sqlite3
import string
from collections import defaultdict
from datetime import datetime
from pathlib import Path
from dotenv import load_dotenv

load_dotenv(Path(__file__).resolve().parent.parent / ".env")

import bcrypt
from fastapi import FastAPI, WebSocket, WebSocketDisconnect, File, UploadFile, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel

# cryptography library — ECDSA verification
from cryptography.hazmat.primitives.asymmetric.ec import (
    ECDSA,
    EllipticCurvePublicKey,
    SECP256R1,
    EllipticCurvePublicNumbers,
)
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric.utils import decode_dss_signature
from cryptography.hazmat.backends import default_backend
from cryptography.exceptions import InvalidSignature
import psutil
import time as _time
import sys
import os

# Add the server directory to sys.path so local modules (feed_store, db) resolve
# when running as a module (-m server.server)
sys.path.append(os.path.dirname(os.path.abspath(__file__)))

import db  # local module — server/db.py
import feed_store # Valkey-based feed store


# ── Environment ───────────────────────────────────────────────────────────────

def _require_env(key: str) -> str:
    value = os.environ.get(key, "").strip()
    if not value:
        raise RuntimeError(
            f"Required environment variable '{key}' is not set in .env"
        )
    return value


AES_GROUP_KEY_HEX: str = _require_env("AES_GROUP_KEY")   # 64 hex chars = 32 bytes
HMAC_SECRET_HEX: str   = _require_env("HMAC_SECRET")     # 64 hex chars = 32 bytes

# ── XP Award Constants ────────────────────────────────────────────────────────
_XP_SEND_MESSAGE  = 10   # XP for sending a message
_XP_RECEIVE_MSG   = 2    # XP for each message received in your room
_XP_SOMEONE_JOINS = 3    # XP awarded to existing members when someone joins
_XP_PER_MINUTE    = 5    # XP per minute spent in a room (heartbeat)
_XP_CREATE_ROOM   = 20   # XP for creating a room
_XP_STREAK_BONUS  = 25   # XP bonus every 10 messages sent


# ── Room Code Generation ───────────────────────────────────────────────────────

_ROOM_CODE_CHARS = string.ascii_uppercase + string.digits

def _generate_room_code() -> str:
    """Generate a unique 6-character alphanumeric room code."""
    for _ in range(20):  # try up to 20 times to avoid collision
        code = "".join(secrets.choice(_ROOM_CODE_CHARS) for _ in range(6))
        if db.get_room(code) is None:
            return code
    raise RuntimeError("Could not generate unique room code after 20 attempts")


# ── ECDSA helpers ─────────────────────────────────────────────────────────────

def _jwk_to_public_key(jwk: dict) -> EllipticCurvePublicKey:
    """
    Convert a JWK (P-256, EC) dict exported by the browser's SubtleCrypto
    into a cryptography library EllipticCurvePublicKey.
    """
    def _b64url_to_int(b64url: str) -> int:
        # Add padding if needed
        padded = b64url + "=" * (-len(b64url) % 4)
        return int.from_bytes(base64.urlsafe_b64decode(padded), "big")

    x = _b64url_to_int(jwk["x"])
    y = _b64url_to_int(jwk["y"])
    numbers = EllipticCurvePublicNumbers(x=x, y=y, curve=SECP256R1())
    return numbers.public_key(default_backend())


def verify_ecdsa_signature(plaintext_bytes: bytes, sig_b64: str, jwk: dict) -> bool:
    """
    Verify an ECDSA-P256/SHA-256 signature.
    `sig_b64`  — base64-encoded DER signature produced by SubtleCrypto.sign()
    `plaintext_bytes` — the original bytes that were signed
    Returns True if valid, False on any error.
    """
    try:
        pub_key = _jwk_to_public_key(jwk)
        # SubtleCrypto outputs the signature in IEEE P1363 format (r||s, 64 bytes).
        # cryptography library expects DER, so we must convert.
        padded = sig_b64 + "=" * (-len(sig_b64) % 4)
        sig_bytes = base64.urlsafe_b64decode(padded)

        if len(sig_bytes) == 64:
            # P1363 → DER
            r = int.from_bytes(sig_bytes[:32], "big")
            s = int.from_bytes(sig_bytes[32:], "big")
            from cryptography.hazmat.primitives.asymmetric.utils import encode_dss_signature
            sig_der = encode_dss_signature(r, s)
        else:
            sig_der = sig_bytes  # already DER

        pub_key.verify(sig_der, plaintext_bytes, ECDSA(hashes.SHA256()))
        return True
    except (InvalidSignature, Exception):
        return False


# ── ConnectionManager ─────────────────────────────────────────────────────────

class ConnectionManager:
    """Manages active WebSocket connections across multiple rooms."""

    def __init__(self):
        # ws → { username, avatar, room_id }
        self.active_connections: dict[WebSocket, dict] = {}

    def add(self, websocket: WebSocket, username: str, avatar: str, room_id: str):
        self.active_connections[websocket] = {
            "username": username,
            "avatar":   avatar,
            "room_id":  room_id,
        }

    def remove(self, websocket: WebSocket) -> tuple[str | None, str | None]:
        """Returns (username, room_id) of the removed connection."""
        info = self.active_connections.pop(websocket, None)
        if info:
            return info["username"], info["room_id"]
        return None, None

    def get_username(self, websocket: WebSocket) -> str | None:
        info = self.active_connections.get(websocket)
        return info["username"] if info else None

    def get_avatar(self, websocket: WebSocket) -> str:
        info = self.active_connections.get(websocket)
        return info["avatar"] if info else "wizard"

    def get_room_id(self, websocket: WebSocket) -> str | None:
        info = self.active_connections.get(websocket)
        return info["room_id"] if info else None

    def get_room_users(self, room_id: str) -> list[dict]:
        """Return unique users in a room (deduplicated by username — same user, multiple tabs → one entry)."""
        seen: dict[str, dict] = {}
        for info in self.active_connections.values():
            if info["room_id"] == room_id:
                key = info["username"].lower()
                if key not in seen:
                    seen[key] = {"username": info["username"], "avatar": info["avatar"]}
        return sorted(seen.values(), key=lambda x: x["username"].lower())

    def get_room_count(self, room_id: str) -> int:
        return sum(1 for info in self.active_connections.values() if info["room_id"] == room_id)

    def is_username_taken_in_room(self, username: str, room_id: str) -> bool:
        return username.lower() in [
            info["username"].lower()
            for info in self.active_connections.values()
            if info["room_id"] == room_id
        ]

    async def broadcast_to_room(
        self, room_id: str, message: dict, exclude: WebSocket | None = None
    ) -> int:
        """Send JSON to all clients in a room (optionally excluding one). Returns delivery count."""
        disconnected = []
        delivered = 0
        for ws, info in self.active_connections.items():
            if info["room_id"] == room_id and ws != exclude:
                try:
                    await ws.send_json(message)
                    delivered += 1
                except Exception:
                    disconnected.append(ws)
        for ws in disconnected:
            self.active_connections.pop(ws, None)
        return delivered

    async def send_to_all_in_room(self, room_id: str, message: dict) -> int:
        return await self.broadcast_to_room(room_id, message, exclude=None)

    async def send_to_user_in_room(self, room_id: str, username: str, message: dict) -> int:
        """Send JSON to ALL connections of a specific user in a room (covers multi-tab). Returns delivery count."""
        delivered = 0
        for ws, info in self.active_connections.items():
            if info["room_id"] == room_id and info["username"].lower() == username.lower():
                try:
                    await ws.send_json(message)
                    delivered += 1
                except Exception:
                    pass
        return delivered


# ── FastAPI App ───────────────────────────────────────────────────────────────

app = FastAPI(title="Secure Group Chat Server")
manager = ConnectionManager()

BACKEND_NAME = os.environ.get("BACKEND_NAME", "unknown")

_in_flight_count = 0
_lag_ewma_ms = 0.0

@app.middleware("http")
async def track_in_flight_and_backend(request, call_next):
    global _in_flight_count
    _in_flight_count += 1
    try:
        response = await call_next(request)
        response.headers["X-Backend-Id"] = BACKEND_NAME
        return response
    finally:
        _in_flight_count -= 1

async def track_event_loop_lag():
    global _lag_ewma_ms
    alpha = 0.3
    while True:
        start = _time.time()
        await asyncio.sleep(0.25)
        lag = max(0.0, (_time.time() - start) - 0.25) * 1000.0
        if _lag_ewma_ms == 0.0:
            _lag_ewma_ms = lag
        else:
            _lag_ewma_ms = alpha * lag + (1 - alpha) * _lag_ewma_ms

# Per-room cleanup tasks: room_id → asyncio.Task
cleanup_tasks: dict[str, asyncio.Task] = {}

# UPLOAD_DIR can be overridden via environment variable so that Sys3 and Sys4
# share the same uploads folder as Sys2 via sshfs:
#   UPLOAD_DIR=/mnt/sys2-server/uploads
UPLOAD_DIR = Path(
    os.environ.get("UPLOAD_DIR", str(Path(__file__).resolve().parent / "uploads"))
)
UPLOAD_DIR.mkdir(parents=True, exist_ok=True)
app.mount("/uploads", StaticFiles(directory=str(UPLOAD_DIR)), name="uploads")

# CORS — allow all for lab purposes
app.add_middleware(
    CORSMiddleware,
    allow_origin_regex=".*",
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)

FRONTEND_PORT = int(os.environ.get("FRONTEND_PORT", 3000))
CLEANUP_TIMEOUT = int(os.environ.get("CLEANUP_TIMEOUT", 300))

@app.get("/health")
async def health_check():
    """Lightweight endpoint used by the frontend and load balancer."""
    try:
        load1, _, _ = os.getloadavg()
    except AttributeError:
        # fallback for environments without getloadavg (e.g. Windows)
        load1 = 0.0
    return {
        "status": "ok",
        "cpu_percent": psutil.cpu_percent(interval=None),
        "load_avg_1m": load1,
        "active_ws_connections": len(manager.active_connections),
        "valkey_rtt_ms": await feed_store.probe_latency(),
        "lag_ms": _lag_ewma_ms,
        "in_flight": _in_flight_count,
        "backend_name": BACKEND_NAME,
    }


# ── Request models ──────────────────────────────────────────────────────────
from pydantic import Field

class SimpleMessageRequest(BaseModel):
    client_name: str = Field(..., alias="client-name")
    msg: str
    msg_id: str | None = None
    class Config:
        populate_by_name = True

DEFAULT_FEED_ROOM = "loadtest-feed"

@app.post("/message")
async def post_message(req: SimpleMessageRequest):
    msg_id = req.msg_id or str(uuid.uuid4())
    inserted = await feed_store.insert_if_new(
        room_id=DEFAULT_FEED_ROOM,
        msg_id=msg_id,
        payload={"client_name": req.client_name, "msg": req.msg, "ts": _time.time()},
    )
    return {"ok": True, "msg_id": msg_id, "duplicate": not inserted}

@app.get("/feed")
async def get_feed(limit: int = 80):
    """
    Retrieve chat messages. By default returns the latest 80 messages.
    Pass ?limit=0 to return all messages (slow when DB is large).
    """
    messages = await feed_store.get_all(DEFAULT_FEED_ROOM, limit=limit)
    return {"messages": messages, "total": len(messages)}


class RegisterRequest(BaseModel):
    username: str
    password: str
    avatar:   str = "wizard"

class LoginRequest(BaseModel):
    username: str
    password: str

class CreateRoomRequest(BaseModel):
    name:       str
    is_public:  bool = True
    avatar:     str = "🏰"
    created_by: str = ""

@app.on_event("startup")
async def startup():
    """Initialise the SQLite database and Valkey feed store on server start."""
    # We only initialize SQLite if we are NOT running behind a proxy that shares it,
    # or if we are the proxy itself. For lab purposes, db.py handles the logic.
    if not os.environ.get("DB_PROXY_URL"):
        db.init_db()
    await feed_store.init_feed_store()
    asyncio.create_task(track_event_loop_lag())


@app.get("/config.js")
async def config_js():
    backend_port = int(os.environ.get("PORT", 8000))
    from fastapi.responses import Response
    return Response(
        content=f"window.PORT = {backend_port};",
        media_type="application/javascript"
    )


# ── Auth Endpoints ────────────────────────────────────────────────────────────


@app.post("/register")
async def register(req: RegisterRequest):
    """
    Create a new user account.
    Hashes the password with bcrypt, saves to DB, returns a one-time session token.
    """
    username = req.username.strip()
    password = req.password
    avatar   = req.avatar

    if not username or len(username) > 20:
        raise HTTPException(status_code=400, detail="Username must be 1-20 characters.")
    if not username.replace("_", "").isalnum():
        raise HTTPException(status_code=400, detail="Username may only contain letters, numbers, and underscores.")
    if not password or len(password) < 6:
        raise HTTPException(status_code=400, detail="Password must be at least 6 characters.")

    pw_hash = bcrypt.hashpw(password.encode("utf-8"), bcrypt.gensalt()).decode("utf-8")

    try:
        db.create_user(username, pw_hash, avatar)
    except sqlite3.IntegrityError:
        print(f"\033[91m[REGISTER ERROR] ❌ Registration failed: Username '@{username}' is already taken.\033[0m")
        raise HTTPException(status_code=409, detail=f"Username '{username}' is already taken.")

    token = secrets.token_hex(32)
    db.save_session_token(token, username, avatar)
    print(f"\033[92m[REGISTER SUCCESS] 🎉 New user '@{username}' registered | Avatar: {avatar} | Password hashed with bcrypt | Session token issued: {token[:8]}...\033[0m")
    return {"token": token, "username": username, "avatar": avatar, "xp": 0}


@app.post("/login")
async def login(req: LoginRequest):
    """
    Authenticate an existing user.
    Verifies bcrypt hash, returns a one-time session token.
    """
    username = req.username.strip()
    password = req.password

    user = db.get_user(username)
    if not user:
        print(f"\033[91m[AUTH ERROR] ❌ Login failed: User '@{username}' not found.\033[0m")
        raise HTTPException(status_code=401, detail="Invalid username or password.")

    if not bcrypt.checkpw(password.encode("utf-8"), user["password_hash"].encode("utf-8")):
        print(f"\033[91m[AUTH ERROR] ❌ Login failed: Invalid password attempt for user '@{username}'.\033[0m")
        raise HTTPException(status_code=401, detail="Invalid username or password.")

    token = secrets.token_hex(32)
    db.save_session_token(token, user["username"], user["avatar"])
    print(f"\033[92m[AUTH SUCCESS] 🔑 Password verified for '@{user['username']}' (bcrypt hash match) | Session token issued: {token[:8]}...\033[0m")
    return {"token": token, "username": user["username"], "avatar": user["avatar"], "xp": user.get("xp", 0)}


class RefreshTokenRequest(BaseModel):
    username: str


@app.post("/refresh-token")
async def refresh_token(req: RefreshTokenRequest):
    """
    Re-issue a one-time session token for a user returning to the lobby.
    No password required — caller must know the username (held in client state).
    Used after leaving a room to join another without re-logging in.
    """
    username = req.username.strip()
    user = db.get_user(username)
    if not user:
        raise HTTPException(status_code=404, detail="User not found.")
    token = secrets.token_hex(32)
    
    # Store token in the global DB so any backend can read it for the WebSocket handshake
    db.save_session_token(token, user["username"], user["avatar"])
    
    print(f"[Auth] Token refreshed for: {user['username']}")
    return {"token": token, "username": user["username"], "avatar": user["avatar"], "xp": user.get("xp", 0)}



# ── Room Endpoints ────────────────────────────────────────────────────────────

@app.get("/rooms")
async def get_rooms():
    """
    Return all public rooms with live online player counts.
    """
    rooms = db.list_rooms()
    for room in rooms:
        room["online"] = manager.get_room_count(room["id"])
    return {"rooms": rooms}


@app.post("/rooms")
async def create_room(req: CreateRoomRequest):
    """
    Create a new chat room. Returns the generated room code.
    The caller must hold a valid session token (passed via X-Session-Token header).
    For simplicity in the lab, we accept any request — room creator is recorded from the name field.
    """
    name = req.name.strip()
    if not name or len(name) > 40:
        raise HTTPException(status_code=400, detail="Room name must be 1-40 characters.")

    room_id = _generate_room_code()
    creator = req.created_by.strip() or "system"
    db.create_room(room_id, name, created_by=creator, is_public=req.is_public, avatar=req.avatar)
    print(f"[Rooms] Created room '{name}' ({room_id}), creator='{creator}', public={req.is_public}")

    # Award XP for room creation if creator is a real user
    xp_total = None
    if creator and creator != "system":
        xp_total = db.add_xp(creator, _XP_CREATE_ROOM)
        print(f"[XP] +{_XP_CREATE_ROOM} XP to {creator} for creating room (total: {xp_total})")

    return {
        "room_id":    room_id,
        "name":       name,
        "created_by": creator,
        "is_public":  req.is_public,
        "avatar":     req.avatar,
        "xp_awarded": _XP_CREATE_ROOM if xp_total is not None else 0,
        "xp_total":   xp_total,
    }


@app.post("/rooms/{room_id}/creator")
async def set_room_creator(room_id: str, body: dict):
    """Update the creator name for a room after the user joins via WebSocket."""
    # This is called optimistically from the client after joining
    # It's best-effort, non-critical
    return {"ok": True}


@app.get("/rooms/{room_id}")
async def get_room(room_id: str):
    """
    Check if a room with the given code exists.
    Returns room metadata or 404.
    """
    room = db.get_room(room_id.upper())
    if not room:
        raise HTTPException(status_code=404, detail=f"Room '{room_id}' not found.")
    room["online"] = manager.get_room_count(room["id"])
    return room


@app.delete("/rooms/{room_id}/history")
async def delete_room_history(room_id: str, body: dict):
    username = body.get("username", "").strip()
    room = db.get_room(room_id)
    if not username or not room or room.get("created_by", "").lower() != username.lower():
        raise HTTPException(status_code=403, detail="Only the room creator can clear history.")
    try:
        await feed_store.delete_room_messages(room_id)
    except Exception as e:
        print(f"\033[91m[ERROR] Failed to delete history from Valkey: {e}\033[0m")
    await manager.send_to_all_in_room(room_id, {
        "type":     "room_history_cleared",
        "room_id":  room_id,
        "username": username,
    })
    return {"ok": True}


@app.delete("/rooms/{room_id}")
async def delete_chat_room(room_id: str, body: dict):
    username = body.get("username", "").strip()
    room = db.get_room(room_id)
    if not username or not room or room.get("created_by", "").lower() != username.lower():
        raise HTTPException(status_code=403, detail="Only the room creator can delete this room.")
    try:
        await feed_store.delete_room_messages(room_id)
    except Exception as e:
        print(f"\033[91m[ERROR] Failed to delete history from Valkey: {e}\033[0m")
    db.delete_room(room_id, username)
    await manager.send_to_all_in_room(room_id, {
        "type":     "room_deleted",
        "room_id":  room_id,
        "username": username,
    })
    return {"ok": True}


# ── REST Endpoints ────────────────────────────────────────────────────────────

@app.get("/group-key")
async def get_group_key():
    """
    Return the AES-GCM group key (hex string) loaded from .env.
    Clients fetch this once on load to initialise SubtleCrypto.
    In production this endpoint should be protected by authentication.
    """
    return {"key": AES_GROUP_KEY_HEX}


@app.get("/users/{username}/xp")
async def get_user_xp(username: str):
    """Return the current XP total for a user."""
    xp = db.get_user_xp(username)
    return {"username": username, "xp": xp}


@app.post("/upload")
async def upload_file(file: UploadFile = File(...)):
    """Handle file upload and return attachment metadata."""
    try:
        ext = Path(file.filename).suffix if file.filename else ""
        unique_name = f"{uuid.uuid4().hex}{ext}"
        file_path = UPLOAD_DIR / unique_name

        with open(file_path, "wb") as buffer:
            shutil.copyfileobj(file.file, buffer)

        file_size = file_path.stat().st_size
        return {
            "url": f"/uploads/{unique_name}",
            "fileName": file.filename or "file",
            "fileType": file.content_type or "application/octet-stream",
            "fileSize": file_size,
        }
    except Exception as e:
        raise HTTPException(status_code=500, detail=f"File upload failed: {str(e)}")


# ── Helpers ───────────────────────────────────────────────────────────────────

def timestamp() -> str:
    return datetime.now().strftime("%H:%M:%S")


def _schedule_room_cleanup(room_id: str):
    """No automatic cleanup — messages persist in database forever unless deleted by room creator."""
    pass


def _cancel_room_cleanup(room_id: str):
    """No-op: automatic cleanup timers are disabled."""
    pass



# ── WebSocket Endpoint ────────────────────────────────────────────────────────

@app.websocket("/ws")
async def websocket_endpoint(websocket: WebSocket):
    """Handle a single client's full lifecycle: join → chat → leave."""
    await websocket.accept()
    username = None
    room_id  = None

    try:
        # ── Wait for join message ────────────────────────────────────────
        data = await websocket.receive_json()

        if data.get("type") != "join":
            await websocket.send_json({
                "type": "error",
                "message": "First message must be a join request.",
            })
            await websocket.close(code=1008)
            return

        # ── Token-based auth ─────────────────────────────────────────────
        token   = data.get("token", "").strip()
        room_id = data.get("room_id", "").strip().upper()

        # 1. Validate token from the global DB
        session = db.consume_session_token(token)
        if not session:
            await websocket.send_json({
                "type":    "error",
                "message": "Invalid or expired session token. Please log in again.",
            })
            await websocket.close(code=1008)
            return

        username = session["username"]
        avatar   = session["avatar"]

        # ── Validate room ─────────────────────────────────────────────────
        if not room_id:
            await websocket.send_json({
                "type":    "error",
                "message": "No room specified. Please select or create a room.",
            })
            await websocket.close(code=1008)
            return

        room = db.get_room(room_id)
        if not room:
            await websocket.send_json({
                "type":    "error",
                "message": f"Room '{room_id}' does not exist.",
            })
            await websocket.close(code=1008)
            return

        pub_key = data.get("public_key")

        # If room creator is currently 'system' or empty, assign it to the joiner
        db.update_room_creator_if_system(room_id, username)
        room = db.get_room(room_id) or room

        # ── Cancel any pending cleanup for this room ──────────────────────
        _cancel_room_cleanup(room_id)

        # Check if the user is already in this room from another tab
        already_in_room = manager.is_username_taken_in_room(username, room_id)

        manager.add(websocket, username, avatar, room_id)
        db.register_user_key(username, pub_key)
        print(f"\033[94m[AUTH WS] 🔐 Authenticated WebSocket session token for '@{username}' | Room: '{room_id}' | ECDSA P-256 Public Key Registered.\033[0m")
        print(f"[+] {username} ({avatar}) joined room '{room_id}' | Online in room: {manager.get_room_count(room_id)}")

        # Welcome message to joiner
        await websocket.send_json({
            "type":      "system",
            "message":   f"Welcome to #{room['name']}, {username}!",
            "timestamp": timestamp(),
            "room":      {"id": room_id, "name": room["name"], "created_by": room["created_by"], "avatar": room.get("avatar", "🏰")},
        })

        # Only announce join to others if this is the user's FIRST connection in this room
        if not already_in_room:
            await manager.broadcast_to_room(room_id, {
                "type":      "join",
                "username":  username,
                "avatar":    avatar,
                "message":   f"{username} joined the room",
                "timestamp": timestamp(),
            }, exclude=websocket)

            # Award XP to all existing room members for someone joining
            for ws_other, info_other in list(manager.active_connections.items()):
                if info_other["room_id"] == room_id and info_other["username"].lower() != username.lower():
                    other_name = info_other["username"]
                    new_xp = db.add_xp(other_name, _XP_SOMEONE_JOINS)
                    try:
                        await ws_other.send_json({
                            "type":   "xp_update",
                            "xp":     new_xp,
                            "gained": _XP_SOMEONE_JOINS,
                            "reason": f"{username} joined",
                        })
                    except Exception:
                        pass
                    print(f"[XP] +{_XP_SOMEONE_JOINS} XP to {other_name} (join event, total: {new_xp})")

        # Updated user list to everyone in room (deduplicated by username)
        await manager.send_to_all_in_room(room_id, {
            "type":  "userList",
            "users": manager.get_room_users(room_id),
        })

        # Send DB-backed history to the new joiner (unlimited history)
        try:
            raw_history = await feed_store.get_all(room_id)
        except Exception as e:
            print(f"\033[91m[ERROR] Failed to fetch history from Valkey: {e}\033[0m")
            raw_history = []
            await websocket.send_json({"type": "error", "message": "Failed to load chat history (database unreachable)."})
        history = []
        for msg in raw_history:
            tgt = msg.get("target_user")
            if tgt and username:
                if msg.get("username", "").lower() != username.lower() and tgt.lower() != username.lower():
                    continue
            history.append(msg)
        if history:
            tampered_count = 0
            valid_count = 0
            for msg in history:
                if not msg.get("sig_valid", True):
                    tampered_count += 1
                    print(f"\033[91m[SECURITY ALERT] ⚠️  Tampered message detected in DB history! Room: '{room_id}' | Sender: '{msg.get('username')}' | Msg ID: '{msg.get('msg_id')}'\033[0m")
                else:
                    valid_count += 1
            print(f"\033[96m[PERSISTENCE] 📂 Chat history loaded from SQLite DB for '@{username}' joining room '{room_id}' | Total messages: {len(history)} | ✅ Integrity OK: {valid_count} | 🚨 Tampered: {tampered_count}\033[0m")
            await websocket.send_json({
                "type":     "history",
                "messages": history,
            })
        else:
            print(f"\033[96m[PERSISTENCE] 📂 No prior history in room '{room_id}' — fresh start for '@{username}'.\033[0m")

        # ── Message loop ─────────────────────────────────────────────────
        while True:
            data = await websocket.receive_json()
            msg_type = data.get("type")

            # ── Chat message ─────────────────────────────────────────────
            if msg_type == "message":
                ciphertext    = data.get("ciphertext", "")
                iv            = data.get("iv", "")
                signature     = data.get("signature", "")
                sender_key    = data.get("public_key") or db.get_user_key(username) or {}
                client_msg_id = data.get("client_msg_id", "")
                attachment    = data.get("attachment")
                reply_to      = data.get("reply_to")      # msg_id of parent (threaded reply)
                target_user   = data.get("target_user")    # username for whispers

                if not ciphertext or not iv or not signature:
                    continue

                # ── Verify ECDSA signature server-side ───────────────────
                signed_material = (ciphertext + iv).encode("utf-8")
                sig_valid = verify_ecdsa_signature(signed_material, signature, sender_key)

                if sig_valid:
                    print(f"\033[92m[AUTH VERIFIED] 🔒 ECDSA-P256 signature VERIFIED for message from '@{username}' in room '{room_id}'.\033[0m")
                else:
                    print(f"\033[91m[SECURITY ALERT] 🚨 INVALID / TAMPERED SIGNATURE! Sender: '{username}' | Room: '{room_id}' | Signature verification failed!\033[0m")

                # ── Persist to DB ─────────────────────────────────────────
                if ciphertext:
                    payload = {
                        "type": "message",
                        "msg_id": client_msg_id,
                        "username": username,
                        "avatar": avatar,
                        "ciphertext": ciphertext,
                        "iv": iv,
                        "signature": signature,
                        "public_key": sender_key,
                        "timestamp": timestamp(),
                        "sig_valid": sig_valid,
                        "reply_to": reply_to,
                        "is_deleted": False,
                        "target_user": target_user,
                        "is_edited": False,
                        "created_at_ts": _time.time(),
                        "attachment": attachment,
                    }
                    # We compute HMAC for the payload just to keep the schema aligned
                    hmac_digest = hmac.new(bytes.fromhex(HMAC_SECRET_HEX), (ciphertext + iv).encode("utf-8"), hashlib.sha256).hexdigest()
                    payload["hmac_digest"] = hmac_digest
                    
                    try:
                        await feed_store.insert_if_new(room_id, client_msg_id, payload)
                        print(f"\033[93m[PERSISTENCE] 💾 Message from '@{username}' stored in Valkey (AES-GCM encrypted, NOT plaintext) | Room: '{room_id}' | IV: {iv[:12]}... | Sig valid: {sig_valid}\033[0m")
                    except Exception as e:
                        print(f"\033[91m[ERROR] Failed to save message to Valkey: {e}\033[0m")
                        await websocket.send_json({"type": "error", "message": "Failed to save message. Please try again."})
                        continue

                # ── Build outbound message ─────────────────────────────────
                msg = {
                    "type":       "message",
                    "msg_id":     client_msg_id,
                    "username":   username,
                    "avatar":     avatar,
                    "ciphertext": ciphertext,
                    "iv":         iv,
                    "signature":  signature,
                    "public_key": sender_key,
                    "timestamp":  timestamp(),
                    "sig_valid":  sig_valid,
                    "attachment": attachment,
                    "reply_to":   reply_to,
                    "target_user": target_user,
                }

                # ── Whisper routing vs broadcast ──────────────────────────
                if target_user:
                    # Whisper: send only to target user (not to sender — they already have optimistic UI)
                    delivered = await manager.send_to_user_in_room(room_id, target_user, msg)
                    receipt_status = "delivered_all" if delivered > 0 else "sent"
                else:
                    # Normal broadcast to entire room
                    delivered = await manager.send_to_all_in_room(room_id, msg)

                    # ── Delivery receipt ──────────────────────────────────
                    room_size      = manager.get_room_count(room_id)
                    others_reached = delivered - 1
                    total_others   = room_size - 1
                    if total_others <= 0:
                        receipt_status = "sent"
                    elif others_reached >= total_others:
                        receipt_status = "delivered_all"
                    else:
                        receipt_status = "partial"

                await websocket.send_json({
                    "type":   "receipt",
                    "msg_id": client_msg_id,
                    "status": receipt_status,
                })

                # ── Award XP to sender for sending a message ──────────────
                # Track per-user message count in connection info for streak
                conn_info = manager.active_connections.get(websocket)
                if conn_info is not None:
                    conn_info["msg_count"] = conn_info.get("msg_count", 0) + 1
                    msg_count = conn_info["msg_count"]
                else:
                    msg_count = 1

                xp_gained = _XP_SEND_MESSAGE
                streak_bonus = 0
                if msg_count % 10 == 0:
                    streak_bonus = _XP_STREAK_BONUS
                    xp_gained += streak_bonus

                new_xp = db.add_xp(username, xp_gained)
                reason = f"+{xp_gained} XP" + (f" (🔥 streak bonus!)" if streak_bonus else "")
                await websocket.send_json({
                    "type":   "xp_update",
                    "xp":     new_xp,
                    "gained": xp_gained,
                    "reason": reason,
                })
                print(f"[XP] +{xp_gained} XP to {username} for sending (total: {new_xp})")

                # ── Award XP to other room members for receiving ───────────
                if not target_user:  # only for normal broadcasts, not whispers
                    seen_recipients = set()
                    for ws_other, info_other in list(manager.active_connections.items()):
                        if (info_other["room_id"] == room_id
                                and info_other["username"].lower() != username.lower()
                                and info_other["username"].lower() not in seen_recipients):
                            other_name = info_other["username"]
                            seen_recipients.add(other_name.lower())
                            other_xp = db.add_xp(other_name, _XP_RECEIVE_MSG)
                            try:
                                await ws_other.send_json({
                                    "type":   "xp_update",
                                    "xp":     other_xp,
                                    "gained": _XP_RECEIVE_MSG,
                                    "reason": f"message received",
                                })
                            except Exception:
                                pass

            # ── Delete / unsend message ───────────────────────────────
            elif msg_type == "delete_message":
                del_msg_id = data.get("msg_id", "")
                if del_msg_id:
                    msg_to_delete = await feed_store.get_msg(room_id, del_msg_id)
                    if msg_to_delete and msg_to_delete.get("username", "").lower() == username.lower() and not msg_to_delete.get("is_deleted"):
                        success = await feed_store.soft_delete(room_id, del_msg_id)
                        if success:
                            # Broadcast tombstone to entire room
                            await manager.send_to_all_in_room(room_id, {
                                "type":     "message_deleted",
                                "msg_id":   del_msg_id,
                                "username": username,
                            })
                        else:
                            await websocket.send_json({
                                "type":    "error",
                                "message": "Could not delete message.",
                            })
                    else:
                        await websocket.send_json({
                            "type":    "error",
                            "message": "Only the sender can delete this message.",
                        })

            # ── Edit message (5-minute window) ────────────────────────
            elif msg_type == "edit_message":
                edit_msg_id = data.get("msg_id", "")
                ciphertext  = data.get("ciphertext", "")
                iv          = data.get("iv", "")
                signature   = data.get("signature", "")
                sender_key  = data.get("public_key") or db.get_user_key(username) or {}

                if edit_msg_id and ciphertext and iv and signature:
                    signed_material = (ciphertext + iv).encode("utf-8")
                    sig_valid = verify_ecdsa_signature(signed_material, signature, sender_key)

                    if not sig_valid:
                        print(f"\033[91m[SECURITY ALERT] 🚨 INVALID / TAMPERED EDIT SIGNATURE! Sender: '{username}' | Room: '{room_id}' | Msg ID: '{edit_msg_id}'\033[0m")

                    msg_to_edit = await feed_store.get_msg(room_id, edit_msg_id)
                    if not msg_to_edit:
                        success, err_msg = False, "Message not found."
                    elif msg_to_edit.get("is_deleted"):
                        success, err_msg = False, "Cannot edit a deleted message."
                    elif msg_to_edit.get("username", "").lower() != username.lower():
                        success, err_msg = False, "You can only edit your own messages."
                    elif msg_to_edit.get("created_at_ts") and (_time.time() - float(msg_to_edit["created_at_ts"]) > 300):
                        success, err_msg = False, "Message edit window (5 minutes) has expired."
                    else:
                        hmac_digest = hmac.new(bytes.fromhex(HMAC_SECRET_HEX), (ciphertext + iv).encode("utf-8"), hashlib.sha256).hexdigest()
                        success = await feed_store.edit_msg(
                            room_id=room_id,
                            msg_id=edit_msg_id,
                            new_ciphertext=ciphertext,
                            new_iv=iv,
                            new_sig=signature,
                            new_hmac=hmac_digest,
                            sig_valid=sig_valid
                        )
                        err_msg = "" if success else "Failed to edit message."

                    if success:
                        # Broadcast edited message payload to entire room
                        await manager.send_to_all_in_room(room_id, {
                            "type":       "message_edited",
                            "msg_id":     edit_msg_id,
                            "username":   username,
                            "ciphertext": ciphertext,
                            "iv":         iv,
                            "signature":  signature,
                            "public_key": sender_key,
                            "sig_valid":  sig_valid,
                            "is_edited":  True,
                        })
                    else:
                        await websocket.send_json({
                            "type":    "error",
                            "message": err_msg,
                        })

            # ── Clear room history (creator only) ──────────────────────
            elif msg_type == "clear_room_history":
                room = db.get_room(room_id)
                success = False
                if room and room.get("created_by", "").lower() == username.lower():
                    await feed_store.delete_room_messages(room_id)
                    success = True
                if success:
                    print(f"[*] History of room '{room_id}' cleared by creator '{username}'")
                    await manager.send_to_all_in_room(room_id, {
                        "type":     "room_history_cleared",
                        "room_id":  room_id,
                        "username": username,
                    })
                else:
                    await websocket.send_json({
                        "type":    "error",
                        "message": "Only the room creator can clear history.",
                    })

            # ── Delete room (creator only) ─────────────────────────────
            elif msg_type == "delete_room":
                room = db.get_room(room_id)
                success = False
                if room and room.get("created_by", "").lower() == username.lower():
                    await feed_store.delete_room_messages(room_id)
                    success = db.delete_room(room_id, username)
                if success:
                    print(f"[!] Room '{room_id}' deleted by creator '{username}'")
                    await manager.send_to_all_in_room(room_id, {
                        "type":     "room_deleted",
                        "room_id":  room_id,
                        "username": username,
                    })
                else:
                    await websocket.send_json({
                        "type":    "error",
                        "message": "Only the room creator can delete this room.",
                    })

            # ── Typing indicator ──────────────────────────────────────
            elif msg_type == "typing":
                await manager.broadcast_to_room(room_id, {
                    "type":     "typing",
                    "username": username,
                }, exclude=websocket)

            # ── Heartbeat (time-in-room XP) ───────────────────────────
            elif msg_type == "heartbeat":
                new_xp = db.add_xp(username, _XP_PER_MINUTE)
                await websocket.send_json({
                    "type":   "xp_update",
                    "xp":     new_xp,
                    "gained": _XP_PER_MINUTE,
                    "reason": "time in room",
                })
                print(f"[XP] +{_XP_PER_MINUTE} XP to {username} (heartbeat, total: {new_xp})")

    except WebSocketDisconnect:
        pass
    except Exception as e:
        print(f"[!] Error for {username or 'unknown'} in room '{room_id}': {e}")
    finally:
        if username and websocket in manager.active_connections:
            manager.remove(websocket)
            online_in_room = manager.get_room_count(room_id) if room_id else 0
            print(f"[-] {username} left room '{room_id}' | Online in room: {online_in_room}")

            if room_id and online_in_room == 0:
                _schedule_room_cleanup(room_id)

            if room_id:
                # Only broadcast "left the room" if the user has NO remaining connections in this room
                user_still_connected = manager.is_username_taken_in_room(username, room_id)
                if not user_still_connected:
                    await manager.broadcast_to_room(room_id, {
                        "type":      "leave",
                        "username":  username,
                        "message":   f"{username} left the room",
                        "timestamp": timestamp(),
                    })
                # Always send the updated (deduplicated) user list
                await manager.send_to_all_in_room(room_id, {
                    "type":  "userList",
                    "users": manager.get_room_users(room_id),
                })


# ── Entry point ───────────────────────────────────────────────────────────────

if __name__ == "__main__":
    import uvicorn

    port = int(os.environ.get("PORT", 5000))

    print("=" * 50)
    print("  Secure Group Chat Server (Multi-Room)")
    print(f"  WebSocket : wss://0.0.0.0:{port}/ws")
    print(f"  Rooms API : GET/POST https://0.0.0.0:{port}/rooms")
    print(f"  Group Key : GET https://0.0.0.0:{port}/group-key")
    print(f"  Frontend  : https://localhost:{FRONTEND_PORT}")
    print("=" * 50)
    BASE_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    uvicorn.run(
        app,
        host="0.0.0.0",
        port=port,
        ssl_keyfile=os.path.join(BASE_DIR, "key.pem"),
        ssl_certfile=os.path.join(BASE_DIR, "cert.pem")
    )
