"""
DB Proxy Server — db_proxy_server.py
======================================
Runs on Sys2 ONLY. Exposes the SQLite database over a simple HTTP API
so that Sys3 and Sys4 can access it without sshfs or any filesystem mount.

Run on Sys2 (in a tmux pane):
    python3 server/db_proxy_server.py

Default port: 6000 (set DB_PROXY_PORT in .env to change)
External access: https://10.1.75.51:6269  (or whatever port is forwarded)

IMPORTANT: This must be started BEFORE the backends on Sys3 and Sys4.
"""

import os
import json
import sqlite3
from pathlib import Path
from dotenv import load_dotenv

BASE_DIR = Path(__file__).resolve().parent.parent
load_dotenv(BASE_DIR / ".env")

# Import the actual db module (runs locally on Sys2 with the real SQLite file)
import db

from fastapi import FastAPI, HTTPException
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel
from typing import Any, Optional
import uvicorn

app = FastAPI(title="DB Proxy — Chat App")

app.add_middleware(
    CORSMiddleware,
    allow_origin_regex=".*",
    allow_credentials=True,
    allow_methods=["*"],
    allow_headers=["*"],
)


# ── Startup ───────────────────────────────────────────────────────────────────

@app.on_event("startup")
async def startup():
    db.init_db()
    print("[DB Proxy] SQLite database initialised.")


# ── Health ────────────────────────────────────────────────────────────────────

@app.get("/health")
def health():
    return {"status": "ok"}


# ── Rooms ─────────────────────────────────────────────────────────────────────

class CreateRoomBody(BaseModel):
    room_id:    str
    name:       str
    created_by: str
    is_public:  bool = True
    avatar:     str  = "🏰"

class UpdateCreatorBody(BaseModel):
    room_id:   str
    username:  str

class DeleteRoomBody(BaseModel):
    room_id:  str
    username: str

class ClearHistoryByCreatorBody(BaseModel):
    room_id:  str
    username: str


@app.post("/db/create_room")
def api_create_room(body: CreateRoomBody):
    try:
        db.create_room(body.room_id, body.name, body.created_by, body.is_public, body.avatar)
        return {"ok": True}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/db/get_room/{room_id}")
def api_get_room(room_id: str):
    result = db.get_room(room_id)
    return {"result": result}


@app.post("/db/update_room_creator")
def api_update_room_creator(body: UpdateCreatorBody):
    db.update_room_creator_if_system(body.room_id, body.username)
    return {"ok": True}


@app.get("/db/list_rooms")
def api_list_rooms():
    return {"result": db.list_rooms()}


@app.post("/db/delete_room")
def api_delete_room(body: DeleteRoomBody):
    result = db.delete_room(body.room_id, body.username)
    return {"result": result}


@app.post("/db/clear_room_history_by_creator")
def api_clear_room_history_by_creator(body: ClearHistoryByCreatorBody):
    result = db.clear_room_history_by_creator(body.room_id, body.username)
    return {"result": result}


# ── Messages ──────────────────────────────────────────────────────────────────

class SaveMessageBody(BaseModel):
    room_id:    str
    username:   str
    avatar:     str
    ciphertext: str
    iv:         str
    signature:  str
    public_key: dict
    timestamp:  str
    sig_valid:  bool
    msg_id:     str              = ""
    reply_to:   Optional[str]   = None
    target_user: Optional[str]  = None
    attachment:  Optional[str]  = None

class GetHistoryBody(BaseModel):
    room_id:  str
    limit:    Optional[int] = None
    username: Optional[str] = None

class DeleteMessageBody(BaseModel):
    msg_id:   str
    username: str

class EditMessageBody(BaseModel):
    msg_id:    str
    username:  str
    ciphertext: str
    iv:         str
    signature:  str
    sig_valid:  bool


@app.post("/db/save_message")
def api_save_message(body: SaveMessageBody):
    try:
        row_id = db.save_message(
            room_id=body.room_id,
            username=body.username,
            avatar=body.avatar,
            ciphertext=body.ciphertext,
            iv=body.iv,
            signature=body.signature,
            public_key=body.public_key,
            timestamp=body.timestamp,
            sig_valid=body.sig_valid,
            msg_id=body.msg_id,
            reply_to=body.reply_to,
            target_user=body.target_user,
            attachment=body.attachment,
        )
        return {"result": row_id}
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.post("/db/get_history")
def api_get_history(body: GetHistoryBody):
    result = db.get_history(body.room_id, body.limit, body.username)
    return {"result": result}


@app.get("/db/get_message_by_id/{msg_id}")
def api_get_message_by_id(msg_id: str):
    result = db.get_message_by_id(msg_id)
    return {"result": result}


@app.post("/db/delete_message")
def api_delete_message(body: DeleteMessageBody):
    result = db.delete_message(body.msg_id, body.username)
    return {"result": result}


@app.post("/db/edit_message")
def api_edit_message(body: EditMessageBody):
    success, err = db.edit_message(
        body.msg_id, body.username,
        body.ciphertext, body.iv, body.signature, body.sig_valid,
    )
    return {"result": success, "error": err}


# ── User keys ─────────────────────────────────────────────────────────────────

class RegisterKeyBody(BaseModel):
    username:   str
    public_key: dict


@app.post("/db/register_user_key")
def api_register_user_key(body: RegisterKeyBody):
    db.register_user_key(body.username, body.public_key)
    return {"ok": True}


@app.get("/db/get_user_key/{username}")
def api_get_user_key(username: str):
    result = db.get_user_key(username)
    return {"result": result}


# ── Users ─────────────────────────────────────────────────────────────────────

class CreateUserBody(BaseModel):
    username:      str
    password_hash: str
    avatar:        str

class AddXpBody(BaseModel):
    username: str
    amount:   int


@app.post("/db/create_user")
def api_create_user(body: CreateUserBody):
    try:
        db.create_user(body.username, body.password_hash, body.avatar)
        return {"ok": True}
    except sqlite3.IntegrityError:
        raise HTTPException(status_code=409, detail=f"Username '{body.username}' already taken.")
    except Exception as e:
        raise HTTPException(status_code=500, detail=str(e))


@app.get("/db/get_user/{username}")
def api_get_user(username: str):
    result = db.get_user(username)
    return {"result": result}


@app.post("/db/add_xp")
def api_add_xp(body: AddXpBody):
    result = db.add_xp(body.username, body.amount)
    return {"result": result}


@app.get("/db/get_user_xp/{username}")
def api_get_user_xp(username: str):
    result = db.get_user_xp(username)
    return {"result": result}


# ── Session Tokens ────────────────────────────────────────────────────────────

class SaveTokenBody(BaseModel):
    token:    str
    username: str
    avatar:   str

@app.post("/db/save_session_token")
def api_save_session_token(body: SaveTokenBody):
    db.save_session_token(body.token, body.username, body.avatar)
    return {"ok": True}

@app.get("/db/consume_session_token/{token}")
def api_consume_session_token(token: str):
    result = db.consume_session_token(token)
    return {"result": result}


# ── Entry point ───────────────────────────────────────────────────────────────

if __name__ == "__main__":
    port = int(os.environ.get("DB_PROXY_PORT", 6000))

    print("=" * 50)
    print("  DB Proxy Server")
    print(f"  Listening on port: {port}")
    print(f"  SQLite DB: {db.DB_PATH}")
    print("  Sys3 and Sys4 should set:")
    print(f"    DB_PROXY_URL=https://10.1.75.51:<forwarded_port>")
    print("=" * 50)

    BASE_DIR = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    uvicorn.run(
        app,
        host="0.0.0.0",
        port=port,
        ssl_keyfile=os.path.join(BASE_DIR, "key.pem"),
        ssl_certfile=os.path.join(BASE_DIR, "cert.pem"),
    )
