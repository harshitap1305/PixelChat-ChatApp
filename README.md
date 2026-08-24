# PixelChat — Group Quest v1.0

A **real-time, gamified group chat** built with **FastAPI WebSockets** (Python) and **Vanilla HTML/CSS/JS**, styled with a retro 8-bit pixel aesthetic.

---

## Features

- ✅ Real-time message broadcasting via WebSockets
- ✅ User join/leave notifications with sound effects (Mario-inspired)
- ✅ **User Authentication** — Secure login/signup system with password validation and database persistence
- ✅ **End-to-End Encryption (E2E)** — Messages, files, media, and voice memos are fully encrypted (Web Crypto API) with signature verification and tamper detection logs
- ✅ Unique usernames — duplicates are rejected server-side
- ✅ **Multi-Room Chat** — Create public/private rooms, join via 6-character unique codes, and view active public rooms in the lobby
- ✅ **Avatar picker** — 12 pre-built pixel avatars (Wizard, Robot, Ninja, Astronaut, Dragon, and more)
- ✅ **Gamification system** — XP, ranks (Newbie → Legend), message streaks, unlockable badges, and level-up toasts
- ✅ **Interactive Feature Guide** — 5-tile grid dashboard showcasing XP, ranks, badges, and chat mechanics
- ✅ **Message receipt indicators** — 😴 Sent · 😃 Partial · 😎 Delivered to all
- ✅ **Optimistic UI** — own messages appear instantly before server confirmation
- ✅ **File & media attachments** — images (inline preview), video, audio, PDFs, and generic files
- ✅ **Voice Memos** — Record and send in-app audio messages seamlessly
- ✅ **Message Management** — Edit your sent messages or delete them permanently
- ✅ **Whispers & Mentions** — Send private messages using `/w @username`, and tag users to highlight and play a mention sound
- ✅ **Threaded Replies** — Reply directly to specific messages for clear conversations
- ✅ **Typing indicator** — shows when another player is typing (with auto-timeout)
- ✅ **Emoji picker** — quick-react panel with 28 emojis
- ✅ **Message history** — last 50 messages replayed for new joiners; auto-cleared 60 s after the room empties (with manual clear/delete options for room owners)
- ✅ **Live online sidebar** — sorted player list with avatars, XP stats, and earned badges
- ✅ Connection status indicator (Online / Offline / Reconnecting)
- ✅ Auto-reconnect with exponential backoff
- ✅ **Multi-tab Persistence** — Sessions are synchronized, allowing seamless operation across multiple browser tabs
- ✅ Info modal explaining receipt emojis
- ✅ Leave button — returns user to the lobby screen cleanly
- ✅ Scanline overlay and retro pixel background

---

## Tech Stack

| Layer     | Technology                                        |
|-----------|---------------------------------------------------|
| Backend   | Python 3 · FastAPI · Uvicorn · python-multipart   |
| Frontend  | HTML5 · Vanilla CSS · Vanilla JS (no frameworks)  |
| Fonts     | Press Start 2P · VT323 (Google Fonts)             |
| Protocol  | WebSockets (RFC 6455) · REST (`POST /upload`)     |

---

## Project Structure

```
group-chat-app/
├── .env                      # Local port config (not committed)
├── .env.example              # Template — copy to create .env
├── server/
│   ├── requirements.txt      # Python dependencies
│   ├── server.py             # FastAPI WebSocket + upload server (port 8000)
│   └── uploads/              # Uploaded files served at /uploads/<uuid>.<ext>
├── client/
│   ├── index.html            # PixelChat UI (login screen + chat screen)
│   ├── style.css             # Retro 8-bit dark theme + all component styles
│   ├── app.js                # WebSocket client, gamification, attachment logic
│   ├── serve.py              # Static file server for frontend (port 5000)
│   └── sounds/               # 8-bit sound effects (join, coin, pipe)
└── README.md
```

---

## Environment Configuration

The app uses a `.env` file at the project root to configure ports.

### 1. Create your `.env`

```bash
cp .env.example .env
```

The default `.env.example`:

```env
# Backend (FastAPI WebSocket + upload server)
BACKEND_PORT=5000

# Frontend (static file server)
FRONTEND_PORT=3000
```

> **Note:** Never commit your `.env` file — it is already in `.gitignore`.

---

## Quick Start

### 1. Install Dependencies

```bash
cd server
pip install -r requirements.txt
```

### 2. Set Up Environment

```bash
# From the project root
cp .env.example .env
```

### 3. Generate SSL Certificates (Required for Crypto)

Because modern browsers enforce strict security for the `crypto.subtle` API, the server must be run over HTTPS.

```bash
# From the project root
python generate_certs.py
```
*(This will generate `cert.pem` and `key.pem` in your project root.)*

Edit `.env` if you need different ports.

### 3. Start the Backend (Terminal 1)

```bash
cd server
python3 server.py
```

The server starts on:
- **WebSocket**: `ws://0.0.0.0:5000/ws`
- **File upload**: `POST http://0.0.0.0:5000/upload`
- **Uploaded files**: `http://0.0.0.0:5000/uploads/<filename>`
- **Room Management APIs**: `GET /rooms`, `POST /rooms`
- **Authentication APIs**: `POST /login`, `POST /register`

### 4. Start the Frontend (Terminal 2)

```bash
cd client
python3 serve.py
```

The frontend is served at `http://0.0.0.0:3000`.

### 5. Open in Browser

```
http://localhost:3000
```

The client automatically connects to `ws://localhost:5000/ws` using `window.location.hostname`.

---

## Lab / Multi-Machine Deployment

1. **Machine 1** (Server): Run both `server.py` and `serve.py` — note its IP (`ip addr` on Linux) (`10.1.75.51` in our case).
2. **Machines 1–4** (Clients): Open `http://10.1.75.51:3269/` in any browser.
3. Each player picks an avatar, enters a unique name, and joins the quest.

> The WebSocket URL is derived from `window.location.hostname` + `BACKEND_PORT`, so clients automatically connect to the correct host with zero configuration.

---

## Architecture

```
┌──────────────────────────────────────────────────────────────┐
│                    Machine 1 (Server)                        │
│                                                              │
│  ┌─────────────────────┐   ┌──────────────────────────────┐  │
│  │  Frontend Server    │   │  Backend Server (FastAPI)    │  │
│  │  client/serve.py    │   │  server/server.py            │  │
│  │  http://0.0.0.0:3000│   │  ws://0.0.0.0:5000/ws        │  │
│  │                     │   │  GET / POST /rooms           │  │
│  │  Serves:            │   │  POST /login, /register      │  │
│  │  index.html         │   │  POST /upload                │  │
│  │  style.css          │   │  GET  /uploads/<file>        │  │
│  │  style.css          │   │  ConnectionManager:          │  │
│  │  app.js             │   │  ├─ broadcast()              │  │
│  │  sounds/            │   │  ├─ add / remove()           │  │
│  └─────────────────────┘   │  ├─ message history (50)     │  │
│                            │                              │ 
│                            └──────────────────────────────┘  │
└────────────────┬───────────────────────┬─────────────────────┘
                 │                       │
          HTTP :5269              WS :5269/ws
          (static files)         (chat + uploads)
                 │                       │
        ┌────────┴────────┐     ┌────────┴────────┐
        │   Browser 1     │     │   Browser 2     │  ...
        │   (Player)      │     │   (Player)      │
        └─────────────────┘     └─────────────────┘
```

---

## Gamification System

| Mechanic      | Detail                                                   |
|---------------|----------------------------------------------------------|
| **XP**        | +2 per message sent · +5 on seeing someone join · +1/min passive |
| **Streak**    | Increments every message; every 5th streak grants +10 XP |
| **Ranks**     | Newbie (0) → Squire (50) → Knight (150) → Champion (320) → Warlord (600) → Legend (1000) |
| **Badges**    | Earned visual badges displayed in the sidebar alongside ranks |
| **Level-up**  | Animated toast + 8-bit ascending chime                   |
| **Stats**     | Messages sent, current streak, session time (all shown in sidebar) |

---

## Message Receipt System

Each message sent shows an emoji receipt that updates once the server confirms delivery:

| Emoji | Status        | Meaning                                      |
|-------|---------------|----------------------------------------------|
| 😴    | `sent`        | Reached the server; no other players online  |
| 😃    | `partial`     | Delivered to some players, but not all       |
| 😎    | `delivered_all` | All players in the room received it        |

The ⓘ button in the chat header opens an in-app modal explaining these receipts.

---

## File Attachment Support

Files are uploaded to the backend via `POST /upload` before sending. The resulting URL is embedded in the message payload.

| File Type     | Rendering                            |
|---------------|--------------------------------------|
| `image/*`     | Inline thumbnail (click to full view)|
| `video/*`     | Inline `<video>` player              |
| `audio/*`     | Inline `<audio>` player              |
| PDF / Word / ZIP / other | Download card with icon and file size |

---

## WebSocket Message Protocol

All messages are JSON with a `type` field.

### Client → Server

| Type      | Fields                                      | Description                        |
|-----------|---------------------------------------------|------------------------------------|
| `join`    | `token`, `public_key`, `room_id`            | Authenticate and join a chat room  |
| `message` | `ciphertext`, `iv`, `signature`, `client_msg_id`, `attachment` | Send an encrypted chat message |
| `typing`  | —                                           | Signal that the user is typing     |

### Server → Client

| Type          | Fields                                            | Description                          |
|---------------|---------------------------------------------------|--------------------------------------|
| `system`      | `message`, `timestamp`                            | System notification (e.g. welcome)   |
| `join`        | `username`, `avatar`, `message`, `timestamp`      | User joined                          |
| `leave`       | `username`, `message`, `timestamp`                | User left                            |
| `message`     | `username`, `avatar`, `ciphertext`, `iv`, `signature`, `public_key`, `attachment`, `timestamp` | Broadcast encrypted chat message         |
| `receipt`     | `msg_id`, `status`                                | Delivery confirmation for a sent message |
| `userList`    | `users` (list of `{username, avatar}`)            | Current online players               |
| `history`     | `messages`                                        | Last ≤50 messages for new joiners    |
| `typing`      | `username`                                        | Another player is typing             |
| `error`       | `message`                                         | Error (e.g., username already taken) |

---
