# Chat protocol, version 2

The console's chat (A242) speaks JSON text frames on two websockets. This
file is what a client needs to take part: the console's own web app, and the
NeoDesk desktop app's chat window on a computer.

Version 1 (the frames of `relay.go` and `frames.go`) stays as it was for
sockets that don't ask for version 2. A message sent in one version reaches
the sockets of the other.

## Sockets

| Who | Address | Proof |
|---|---|---|
| A computer's chat window | `wss://<console>/ws/chat/<device id>?v=2&token=<device token>` (or the token as `Authorization: Bearer <token>`) | the device token bound to that device id, the one the client uses for `/api/nd/...` |
| A console user | `wss://<console>/ws/chat-operator/panel?v=2` | the console session cookie; the account needs `chat.support` or `chat.team` |

Without `v=2` a socket speaks version 1. A refused upgrade answers 403 (bad
token, foreign origin, no permission), 401 (no session), 404 (`v=2` on a room
other than `panel`) or 429 (too many sockets). The server pings every 30 s
and closes a socket that hasn't answered for 60 s. Frames over 8192 bytes
close the socket with 1009. A computer's socket may send 40 frames at once
and 4 a second after that; the rest are dropped.

A second socket of the same computer replaces nothing in version 2: every
open socket of the device gets every frame.

## Objects

```json
// A party: who wrote, read or reacted
{"kind": "device" | "user" | "system", "id": "900100001" | "7", "name": "reception-01" | "Anna Weber"}

// A message
{
  "id": "412",                       // the stored id, as text; ids grow with time
  "conversation_id": "900100001",    // a device id, "dm:<low account id>:<high>", "group:<8 hex>"
  "from": <party>,
  "text": "Hello",                   // "" for a file-only message and for a deleted one
  "reply_to": {"id": "405", "from_name": "Anna", "preview": "first 140 characters", "deleted": false} | null,
  "attachments": [{"id": "<32 hex>", "name": "shot.png", "type": "image/png", "size": 12345,
                   "width": 800, "height": 600, "url": "/api/chat/files/<id>"}],
  "created_at": 1795563496110,       // ms since 1970, UTC
  "edited_at": null | 1795563499000,
  "deleted": false,
  "reactions": {"👍": [<party>, ...]},
  "client_id": "c1"                  // the sender's own id for the message, echoed back
}
```

- A computer reads files at `/api/chat/device/files/<id>` with its device
  token (`Authorization: Bearer`), not at the `url` above (which is for console
  sessions). Add `?inline=1` to get a picture (`image/png`, `image/jpeg`,
  `image/gif`, `image/webp` only) to show; anything else always downloads.
- A deleted message keeps its place with `deleted: true` and no text or files.
- Lines of the system (`from.kind = "system"`, e.g. "Anna took the
  conversation") go to console users only; a computer never gets them.
- Text is at most 4096 characters; a message carries at most 10 files of at
  most 50 MB each.

## Frames a client sends

| type | fields | what it does |
|---|---|---|
| `send` | `conversation_id`, `text`, `reply_to` (message id or ""), `attachments` (list of file ids), `client_id` | stores a message; everyone of the conversation gets `message.new`, the sender too (with its `client_id`) |
| `edit` | `message_id`, `text` | the sender changes its own message within 24 hours: `message.updated` |
| `delete` | `message_id` | the sender (or a console admin) deletes it: `message.updated` with `deleted: true` |
| `react` | `message_id`, `emoji`, `on` (true adds, false takes back) | `message.updated` |
| `read` | `conversation_id`, `up_to` (message id; "" or 0 for the newest) | moves the reader's read mark forward: `read` to everyone of the conversation |
| `typing` | `conversation_id` | `typing` to the others; send at most every 2.5 s while typing |
| `history` | `conversation_id`, `before` (message id, "0" for the newest page), `limit` (1 to 100, default 50) | answers `history` |
| `presence` | `status`: `online`, `away`, `busy` | console users only: `presence` to Team chat users |
| `session_line` | `conversation_id`, `text` | console users only: a line the person wrote in a remote session's chat (see below) |

A computer's socket may leave out `conversation_id`: its frames always go to
its own conversation (its device id), whatever they name.

## Frames the server sends

| type | fields |
|---|---|
| `welcome` | `v: 2`, `capabilities`, `server_time`; a console user also `me {id, name, username}` and `may {support, team, assign, delete_any}`; a computer `device_id`, `name` |
| `history` | `conversation_id`, `messages` (oldest first), `reads` ([{`reader` {kind, id}, `up_to`}]), `more` (older messages exist), `before` |
| `message.new` | `message` |
| `message.updated` | `message` (the whole message as it is now) |
| `read` | `conversation_id`, `reader` {kind, id}, `up_to` |
| `typing` | `conversation_id`, `from` (party) |
| `helper` | computers only: `name` (who in the console has the conversation, null for nobody yet) and `state` (`open` or `closed`); sent after the welcome and whenever it changes |
| `presence` | console only: `user_id`, `status` (`online`, `away`, `busy`, `offline`) |
| `device` | console only: `device_id`, `chat_connected` (a chat window of version 2 is open) |
| `conversation.updated` | console only: `conversation` {id, kind, state, assignee_id, assignee_name, waiting_since, closed_at, closed_by, close_note, help_request_id, ticket_id, ...} |
| `notice` | console only: `code: "taken_over"`, `conversation_id`, `by` |
| `error` | `code`, `for` (the frame's type), `client_id` when the frame had one |

Error codes: `no_access` (a conversation the sender may not read),
`not_found`, `not_yours`, `too_late` (edit after 24 hours), `deleted`,
`empty` (no text and no files), `bad_attachment` (a file id that isn't the
sender's own upload), `history_failed`, `failed`.

Unknown frame types and unknown fields are ignored on both sides; a client
must do the same, so later versions can add them.

## A computer's chat window, step by step

1. Open `/ws/chat/<id>?v=2` with the device token. The server answers
   `welcome`, then `history` (the newest 50 messages and the read marks),
   then `helper`.
2. Show the messages. Messages `from.kind == "user"` are the console's; show
   `from.name`. `helper.name` says who is helping ("Anna Weber is helping
   you"); null means "Waiting for someone".
3. Send with `send` and a `client_id`; show the message as sending until
   `message.new` comes back with that `client_id`, or `error` with it.
4. When the window shows the newest message, send `read` (no `up_to`). A
   `read` frame from a `user` reader with `up_to` >= one of the person's
   messages means "Seen".
5. Files: `POST /api/chat/device/files` (multipart, field `file`, device
   token as bearer), answer `{"file_id": "<32 hex>", ...}`; then `send` with
   `attachments: ["<id>"]`. Pasted screenshots are files like any other.
6. Notify (OS notification, sound, tray badge) on `message.new` from a user
   while the window isn't focused.
7. On close, reconnect with backoff (1 s doubling to 30 s) and ask `history`
   again; merge by message id.

## What older and other clients see

- **Console pages and agents of version 1** (`/ws/chat-operator/<id>`,
  `/ws/chat/<id>` without `v=2`) get version 2 messages as frames of version 1
  (`{"type":"message","from":"operator","from_name":...,"text":...}`), text
  only; files arrive as their name. Replies, edits, deletions and reactions
  don't reach them.
- **CDAP agents** get a console user's message to their device as
  `chat_message` over CDAP, as before; their own `chat_message` lands in their
  device's conversation and reaches the console at once.
- **The NeoDesk desktop app before the chat window** and **stock RustDesk
  clients** take part only inside a remote session, through RustDesk's own
  `ChatMessage {text}`: the console's viewer (lib/chat/session-bridge.ts)
  stores what the person writes there in the device's conversation
  (`session_line`) and passes the console's messages into the session as
  plain text; a file goes in as `File: <name>` without a link. Outside a
  session they can't be reached; the console says so and keeps the messages
  for later.

## HTTP routes (console sessions)

All under `/api/chat`, answering JSON with `success`:

| Route | Permission | What |
|---|---|---|
| `GET /inbox?filter=open|mine|closed&q=` | chat.support | support conversations with their computer, newest message, unread count; `counts` |
| `GET /devices?q=` | chat.support | computers to start a conversation with |
| `GET /conversations/{id}` | either | a conversation with its computer (and `assignable` people) or its people |
| `POST /conversations/{id}/assign {account_id}` | chat.support and chat.assign | 0 gives it back to the inbox |
| `POST /conversations/{id}/close {note, resolve_help_request}` | chat.support; the assignee or chat.assign | |
| `POST /conversations/{id}/reopen`, `/link {ticket_id, help_request_id}` | chat.support | |
| `GET /team` | chat.team | direct and group conversations, and the colleagues one may write to |
| `POST /team/direct {account_id}`, `POST /team/groups {title, members}`, `PATCH /team/groups/{id}` | chat.team | |
| `GET /summary` | either | unread counts for the badges |
| `GET/POST /canned`, `PATCH/DELETE /canned/{id}` | either; shared replies need chat.canned_manage | |
| `GET/PUT /me` | either | the account's notifications {browser, sound, mail} |
| `GET /settings`, `PUT /settings` | either; server.config to change | {mail_after_minutes, mail_preview} |
| `GET /messages/{id}/trail` | audit.view | earlier texts of an edited or deleted message |
| `POST /upload`, `GET /files/{id}[?inline=1]`, `GET /search` | either | files and search |
| `POST /device/files`, `GET /device/files/{id}` | device token | a computer's files |
