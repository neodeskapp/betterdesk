# Chat protocol, version 2

The console's chat (A242) speaks JSON text frames on websockets. This file
is everything a client needs to take part without reading the server's
code: the console's own web app, and the NeoDesk desktop app's chat window
on a computer. Sections marked "console only" can be skipped by the
desktop app.

Version 1 (the frames of `relay.go` and `frames.go`) stays as it was for
sockets that don't ask for version 2. A message sent in one version
reaches the sockets of the other (see "What older and other clients see").

## Contents

1. Connecting
2. Objects
3. Frames a client sends, with examples
4. Frames the server sends, with examples
5. Errors
6. Limits
7. Reconnecting and catching up
8. Files
9. What a computer's chat window must show
10. A computer's chat window, step by step
11. What older and other clients see
12. HTTP routes (console sessions)

## 1. Connecting

| Who | Address |
|---|---|
| A computer's chat window | `ws(s)://<API server>/ws/chat/<device id>?v=2&token=<device token>`, or the same path at the console's address |
| A console user (console only) | `wss://<console host>/ws/chat-operator/panel?v=2` |

A computer may use either of two addresses:

- **The server's API port**, where it sends everything else (`/api/nd/...`,
  the heartbeat): its API server setting, or the API port next to the ID
  server (21121 by default). This is the address to use. The API port
  answers only a computer's chat here: the socket of version 2 and the file
  routes of section 8; nothing else of the console.
- **The console's address** (`https://<console host>`), for a computer
  behind a proxy that sends it there. It answers the same.

Use `ws://` where the address is plain HTTP and `wss://` where it is HTTPS,
as for the computer's other calls to it.

**A computer's proof** is its device token, the one it got when it
registered and already sends to `/api/nd/...`. The token is bound to one
device id; it must match the id in the path. Send it as the `token` query
parameter (URL-encoded) or as a header `Authorization: Bearer <token>`. The
query wins when both are there.

**Origin.** A native client sends no `Origin` header and no session
cookie; that is allowed. A client that does send `Origin` must send the
address it connects to (`https://<host>`), else the upgrade is refused.

**A console user's proof** is the session cookie of the console; the
account needs Support chat (`chat.support`) or Team chat (`chat.team`).

**Refused upgrades** answer with an HTTP status and no websocket:

| Status | Why | What the client does |
|---|---|---|
| 401 | a console socket without a signed-in session; a computer that reached an API port without chat (older servers only) | a console user: sign in again; a computer: no chat there, check again after some minutes |
| 403 | wrong or revoked token, a token for another device, a foreign `Origin`, the id `panel` on `/ws/chat/`, an account without a chat permission | a computer: the token is refused; check again after some minutes, and at once with a new token |
| 404 | a computer's socket on a server that has no chat of version 2 (also a server whose console is off), or without `v=2` on the API port; a console socket with `v=2` on a room other than `panel` | a computer: no chat on this server, check again after some minutes |
| 503 with `X-NeoDesk-Chat: starting` | the API port while the server starts or stops and its console isn't up yet | ask again after `Retry-After` seconds (5), with the usual backoff |
| 503 with `X-NeoDesk-Chat: chat_off` | a computer's socket while an admin has "Chat with computers" off | no chat button; check again after `Retry-After` seconds (600) |
| 429 | the token already has 4 chat sockets open | close old sockets; retry after 30 s |
| 500 | the token could not be checked | retry with backoff |

Without `v=2` the socket speaks version 1. Every open socket of a computer
gets every frame of version 2; a second window doesn't push out the first.

**Is there chat on this server?** An admin can turn "Chat with
computers" off (on by default). Then a computer's socket of version 2 is
refused with this answer, and sockets already open are closed with code
1008 and the reason `chat_off`:

```
HTTP/1.1 503 Service Unavailable
Content-Type: application/json
Retry-After: 600
X-NeoDesk-Chat: chat_off

{"success":false,"error":"Chat with computers is off","code":"chat_off"}
```

The client shows no chat button and asks again after `Retry-After`
seconds. Nothing else turns chat off for a computer: its socket doesn't
depend on any account's permissions. What a computer can meet:

- The upgrade succeeds and the first frame is `welcome` with `v: 2`: chat
  works.
- 503 with `chat_off`, or a close with 1008 `chat_off`: chat with
  computers is off; no chat button, ask again after some minutes.
- The upgrade succeeds but the first frame is anything else (an older
  server ignores `v=2` and speaks version 1, starting with `history` or
  `contacts`): close the socket and treat it as no chat; check again after
  some minutes.
- 503 with `starting`: the server is starting or stopping; ask again
  after `Retry-After` seconds (a few), not after minutes.
- 404: no chat of version 2 on this server (or its console is off);
  check again after some minutes.
- 401: an older server's API port, which had no chat; check again after
  some minutes.
- 403: the token is refused; check again after some minutes, and at once
  when the computer has a new token.
- An open socket closed with code 4403 and the reason `token_refused`: the
  computer's credential ended while it was connected (its token revoked
  or replaced, its key replaced, the computer deleted or banned). Treat it
  as a 403. The server closes such sockets as soon as it learns of the
  end, and checks every computer's socket again once a minute.
- No device token yet (the computer isn't registered or approved): don't
  connect.

**Staying reachable.** The console shows a computer as reachable for chat
(the `device` frame's `chat_connected`, "The person sees your messages in
the NeoDesk app now") only while at least one socket of version 2 of that
computer is open. A client should hold the socket for as long as it runs,
also while its chat window is closed, and ideally from a process that runs
from login on (the service or the tray), so messages reach the person and
can raise a notification. When no socket is open the console says "not
reachable" and keeps the messages for later.

**Keepalive.** The server sends a websocket ping every 30 s and closes a
socket that hasn't answered pings for 60 s. Websocket libraries answer
pings themselves; a client needs no frames of its own to stay connected.

## 2. Objects

All ids are JSON strings, also where they look like numbers. Times are
milliseconds since 1970 in UTC, as JSON numbers.

### Party: who wrote, read or reacted

```json
{"kind": "device", "id": "900100001", "name": "reception-01"}
{"kind": "user",   "id": "7",         "name": "Anna Weber"}
{"kind": "system", "id": "",          "name": ""}
```

`device` is a computer (`id` is its device id), `user` a console account
(`id` is the account id, `name` the first and last name, else the user
name), `system` the console itself.

### Message

```json
{
  "id": "412",
  "conversation_id": "900100001",
  "from": {"kind": "user", "id": "7", "name": "Anna Weber"},
  "text": "Hi, I am looking at it now.",
  "reply_to": {"id": "405", "from_name": "reception-01", "preview": "The printer blinks orange", "deleted": false},
  "attachments": [
    {"id": "4f0c2b8e9d1a4c6b8e2f7a9c0d1e3f5a", "name": "error.png", "type": "image/png",
     "size": 48213, "width": 800, "height": 600, "url": "/api/chat/files/4f0c2b8e9d1a4c6b8e2f7a9c0d1e3f5a"}
  ],
  "created_at": 1795563496110,
  "edited_at": null,
  "deleted": false,
  "reactions": {"👍": [{"kind": "device", "id": "900100001", "name": "reception-01"}]},
  "client_id": "c-17"
}
```

| Field | Meaning |
|---|---|
| `id` | the stored id; ids grow with time, so a higher id is newer |
| `conversation_id` | a device id (a computer's conversation), `dm:<lower account id>:<higher>` (two colleagues), `group:<8 hex>` (a group of colleagues), `team:<8 hex>` (a team, see section 12) or `old:<id of version 1>` (console only, see below) |
| `text` | plain text, at most 4096 characters; `""` for a message of files only and for a deleted one |
| `reply_to` | the message this one answers, or `null`; `preview` is the first 140 characters of its text, or the first file's name for a message of files only |
| `attachments` | files, at most 10; `width` and `height` only for pictures; `url` is for console sessions (a computer uses section 8) |
| `edited_at` | when the text was last changed, or `null` |
| `deleted` | `true` keeps the message's place with no text and no files |
| `reactions` | emoji to the parties who reacted with it; `{}` for none |
| `client_id` | the sender's own id for the message, given back to everyone; may be `""` |

A computer's conversation has exactly one computer: its own. Lines of the
system (`from.kind == "system"`, such as "Anna took the conversation") go
to console users only; a computer never gets them.

`old:` conversations are history of version 1 that belongs to no two
accounts. Only admins read them and nobody writes in them.

## 3. Frames a client sends

Every frame is one JSON object with a `type`. Fields not listed are
ignored. A computer may leave out `conversation_id` everywhere: its frames
always go to its own conversation, whatever they name.

### send

Stores a message. Everyone who reads the conversation gets
`message.new`, the sender too, with the sender's `client_id`.

```json
{"type": "send", "text": "The printer blinks orange", "client_id": "c-17"}
{"type": "send", "conversation_id": "900100001", "text": "See the picture", "reply_to": "405",
 "attachments": ["4f0c2b8e9d1a4c6b8e2f7a9c0d1e3f5a"], "client_id": "c-18"}
```

`reply_to` names a message of the same conversation (anything else is
ignored). `attachments` are ids the sender uploaded itself (section 8).
Text is trimmed at the end and cut at 4096 characters.

### edit

The sender changes the text of its own message, within 24 hours of
sending it. Everyone gets `message.updated`.

```json
{"type": "edit", "message_id": "412", "text": "The printer blinks red"}
```

### delete

The sender deletes its own message at any time (a console admin deletes
any). Everyone gets `message.updated` with `deleted: true`. The console
keeps the old text in a trail only admins see.

```json
{"type": "delete", "message_id": "412"}
```

### react

Adds (`on: true`, the default) or takes back (`on: false`) a reaction.
Everyone gets `message.updated`.

```json
{"type": "react", "message_id": "412", "emoji": "👍", "on": true}
```

### read

Moves the sender's read mark forward to `up_to`, or to the newest message
when `up_to` is missing, `""` or `"0"`. A mark never moves back. Everyone
gets `read`.

```json
{"type": "read"}
{"type": "read", "conversation_id": "dm:5:7", "up_to": "412"}
```

### typing

Says the sender is typing. Send it at most every 2.5 s while the person
types; the others show it for 4 s after the last one.

```json
{"type": "typing"}
```

### history

Asks for a page of older messages. The answer is `history`.

```json
{"type": "history", "before": "0", "limit": 50}
{"type": "history", "conversation_id": "900100001", "before": "363", "limit": 50}
```

`before` is a message id (`"0"` or missing for the newest page); `limit` is
1 to 100, 50 when missing or out of range.

### presence (console only)

```json
{"type": "presence", "status": "away"}
```

`online`, `away` or `busy`; Team chat users get `presence`.

### session_line (console only)

A line the person at a computer wrote in a remote session's chat, which
only the console user's viewer sees (the session is end to end). It goes
into the computer's conversation as the computer's message; the same text
from a second viewer within 5 s is dropped.

```json
{"type": "session_line", "conversation_id": "900100001", "text": "thanks, it works"}
```

## 4. Frames the server sends

### welcome

The first frame on every socket.

```json
{"type": "welcome", "v": 2, "server_time": 1795563496110,
 "capabilities": ["v2", "reply", "edit", "delete", "attachments", "reactions", "read_marks", "typing", "presence", "inbox", "assign"],
 "device_id": "900100001", "name": "reception-01"}
```

A console user gets `me` and `may` in place of `device_id` and `name`:

```json
{"type": "welcome", "v": 2, "server_time": 1795563496110, "capabilities": ["v2", "..."],
 "me": {"id": 7, "name": "Anna Weber", "username": "anna"},
 "may": {"support": true, "team": true, "assign": true, "delete_any": false}}
```

`server_time` lets a client correct its clock when it shows times.

### history

The answer to `history`, and on a computer's socket the second frame
after `welcome` (its newest 50 messages).

```json
{"type": "history", "conversation_id": "900100001", "before": "0", "more": true,
 "messages": [<message>, <message>],
 "reads": [{"reader": {"kind": "user", "id": "7"}, "up_to": "412"},
           {"reader": {"kind": "device", "id": "900100001"}, "up_to": "410"}]}
```

`messages` are oldest first. `more` says older messages exist: ask again
with `before` set to the id of the oldest one you have. `reads` are the
read marks of everyone who read the conversation.

### message.new

```json
{"type": "message.new", "message": <message>}
```

### message.updated

The whole message as it is now, after an edit, a deletion or a reaction.
Replace your copy by `id`.

```json
{"type": "message.updated", "message": <message>}
```

### read

```json
{"type": "read", "conversation_id": "900100001", "reader": {"kind": "user", "id": "7"}, "up_to": "412"}
```

### typing

```json
{"type": "typing", "conversation_id": "900100001", "from": {"kind": "user", "id": "7", "name": "Anna Weber"}}
```

### helper (computers only)

Who in the console has the computer's conversation, and whether it is
open. Sent after `history` and whenever it changes.

```json
{"type": "helper", "name": "Anna Weber", "state": "open"}
{"type": "helper", "name": null, "state": "open"}
{"type": "helper", "name": "Anna Weber", "state": "closed"}
```

`name: null` means nobody took it yet. `closed` means the console closed
the conversation; the person's next message opens it again.

### Console only

| Frame | Example |
|---|---|
| `presence` | `{"type":"presence","user_id":7,"status":"away"}` (`online`, `away`, `busy`, `offline`) |
| `device` | `{"type":"device","device_id":"900100001","chat_connected":true}`: a chat window of version 2 opened or closed |
| `conversation.updated` | `{"type":"conversation.updated","conversation":{"id":"900100001","kind":"support","state":"open","assignee_id":7,"assignee_name":"Anna Weber","last_message_id":"412","last_message_at":1795563496110,"waiting_since":1795563400000,"help_request_id":12}}` |
| `notice` | `{"type":"notice","code":"taken_over","conversation_id":"900100001","by":"Ben Kraus"}` |
| `conversation.removed` | `{"type":"conversation.removed","conversation_id":"team:1a2b3c4d"}`: a team was deleted or the account is no longer in it; drop it |

## 5. Errors

A frame the server can't carry out is answered with `error` to the sender
only, and changes nothing:

```json
{"type": "error", "code": "too_late", "for": "edit", "message_id": "412"}
{"type": "error", "code": "bad_attachment", "for": "send", "client_id": "c-18"}
```

`for` is the type of the frame; `client_id` and `message_id` are there when
the frame had them, so a client knows which message failed.

| Code | When | What the client does |
|---|---|---|
| `no_access` | a conversation the sender may not read | drop the conversation |
| `not_found` | a message id that doesn't exist or isn't readable | drop the action |
| `not_yours` | editing or deleting someone else's message | hide edit and delete for others' messages |
| `too_late` | an edit after 24 hours | hide edit after 24 hours |
| `deleted` | editing or reacting to a deleted message | |
| `empty` | `send` with no text and no files, or an edit to no text | |
| `bad_attachment` | a file id that isn't the sender's own upload, or more than 10 | upload again |
| `read_only` | writing in old messages of version 1 (console only) | |
| `failed` | the server couldn't store it | offer "try again" |

Frames the server doesn't understand (not JSON, binary, an unknown
`type`) are ignored without an answer. Unknown fields are ignored on both
sides; a client must do the same, so later versions can add fields and
frames.

## 6. Limits

| What | Limit |
|---|---|
| One frame | 32768 bytes on a socket of version 2 (8192 on version 1); a larger one closes the socket with code 1009. A message of 4096 characters always fits |
| Frames from a computer | 40 at once, then 4 a second; frames over that are dropped without an answer |
| Text | 4096 characters (longer text is cut) |
| Files per message | 10 |
| One file | 50 MB |
| All chat files of the console | a set total; past it uploads answer 507 |
| History page | 1 to 100 messages |
| Editing | 24 hours after sending |
| Chat sockets per device token | 4 |

## 7. Reconnecting and catching up

- When the socket closes, reconnect with backoff: 1 s, doubling to 30 s,
  with some random spread. Stop on 403 (section 1).
- After `welcome` a computer gets its newest 50 messages again. Merge them
  with what you have by message `id` (replace yours with the server's).
  When the newest message you had before isn't among them and `more` is
  true, page back with `history` until you reach it, so nothing that came
  while you were away is missing.
- Read marks come with every `history`; take the highest `up_to` per
  reader.
- Messages you sent that never came back (no `message.new` or `error`
  with their `client_id`) may or may not be stored: the server doesn't
  drop a repeated `client_id`. Look for the `client_id` in the history
  first, and send again only when it isn't there.
- Send `read` again after catching up when the window shows the newest
  message.

## 8. Files

A computer uploads and downloads with its device token, at the same
address as its socket (the API port, or the console's address):

```
POST /api/chat/device/files
Authorization: Bearer <device token>
Content-Type: multipart/form-data; boundary=...

(one part, field name "file", with a file name)
```

The answer:

```json
{"success": true, "file_id": "4f0c2b8e9d1a4c6b8e2f7a9c0d1e3f5a", "filename": "4f0c2b8e9d1a4c6b8e2f7a9c0d1e3f5a.png", "size": 48213, "url": "/api/chat/files/4f0c2b8e9d1a4c6b8e2f7a9c0d1e3f5a"}
```

Then `send` with `attachments: ["<file_id>"]`. Pasted screenshots are
files like any other. A failed upload answers `400 {"success":false,"error":"No file provided"}`
(no file), `401` (no token, or one that is not this computer's device token), `507` (the console's chat files are
full) or `500` (a broken form or a file over 50 MB).

To show or save a file of a message, ask

```
GET /api/chat/device/files/<attachment id>            the file as a download
GET /api/chat/device/files/<attachment id>?inline=1   a picture to show
Authorization: Bearer <device token>
```

`?inline=1` gives PNG, JPEG, GIF and WebP pictures with their own type to
show in the window; any other file comes as a download. A computer gets
only files of its own conversation: anything else answers 404. The
server checks a file's type by its bytes, not its name; text files come
as `text/plain`.

## 9. What a computer's chat window must show

- **Messages** in order of `id`, grouped by day. A message `from.kind ==
  "user"` is the console's: show `from.name`. The computer's own messages
  are the person's.
- **Text** as plain text with line breaks. Make `http://` and `https://`
  addresses clickable links and nothing else: no Markdown, no HTML, no
  previews of links.
- **A reply** shows the quoted message above the text: `reply_to.from_name`
  and `reply_to.preview`, or "Message deleted" when `reply_to.deleted`.
  Clicking it scrolls to that message when it is loaded.
- **Files:** pictures (`width` and `height` set) as a thumbnail from
  `?inline=1` that opens large; other files as a row with the name, the
  size and a download button.
- **Edited:** a small "edited" beside the time when `edited_at` is set.
- **Deleted:** "Message deleted" in place of the text, in a muted style.
- **Reactions:** each emoji with its count under the message; one's own
  reaction marked. Clicking an emoji toggles one's own.
- **Who helps:** from `helper`: "Anna Weber is helping you", or "Waiting
  for someone" when `name` is null, and "This conversation was closed.
  Write to open it again." when `state` is `closed`.
- **Typing:** "Anna Weber is typing" for 4 s after a `typing` frame.
- **Seen:** under the person's newest message, "Seen" once a `read` from
  a `user` reader has `up_to` at or past it.
- **Sending:** a message shows as sending until `message.new` comes back
  with its `client_id`; "Not sent, try again" on an `error` with it.
- **Not connected:** say so while the socket is closed and keep what the
  person typed.
- **Notifications:** on `message.new` from a `user` while the window isn't
  in front, an OS notification with the sender's name and the start of
  the text, a sound if the user wants one, and a badge on the tray icon
  until the window shows the message.

The person's own actions: send text and files, reply, edit their own
messages within 24 hours, delete their own messages, react. They never
see lines of the system and never see the console's other conversations.

## 10. A computer's chat window, step by step

1. Open `/ws/chat/<id>?v=2` with the device token. The server answers
   `welcome`, then `history` (the newest 50 messages and the read marks),
   then `helper`.
2. Show the messages as in section 9.
3. Send with `send` and a new `client_id` per message.
4. When the window shows the newest message, send `read` (no `up_to`).
5. Files: upload, then `send` with the file ids (section 8).
6. Notify while the window isn't in front (section 9).
7. On close, reconnect and catch up (section 7).
8. Keep the socket open while the app runs, also with the window closed,
   so the console shows the computer as reachable (section 1).

## 11. What older and other clients see

- **Console pages and agents of version 1** (`/ws/chat-operator/<id>`,
  `/ws/chat/<id>` without `v=2`) get messages of version 2 as frames of
  version 1 (`{"type":"message","from":"operator","from_name":...,"text":...}`),
  text only; files arrive as their name. Replies, edits, deletions and
  reactions don't reach them.
- **CDAP agents** get a console user's message to their device as
  `chat_message` over CDAP, as before; their own `chat_message` lands in
  their device's conversation and reaches the console at once.
- **The NeoDesk desktop app before the chat window** and **stock RustDesk
  clients** take part only inside a remote session, through RustDesk's own
  `ChatMessage {text}`: the console's viewer (lib/chat/session-bridge.ts)
  stores what the person writes there in the device's conversation
  (`session_line`) and passes the console's messages into the session as
  plain text; a file goes in as `File: <name>` without a link. Outside a
  session they can't be reached; the console says "not reachable" and
  keeps the messages for later.
- **History of version 1** is moved once, at the first start of version 2:
  direct messages into the direct conversation of their two people,
  groups with two or more accounts into groups, a computer's messages
  without a conversation into the computer's, and the rest into `old:`
  conversations for admins.

## 12. HTTP routes (console sessions, console only)

All under `/api/chat`, answering JSON with `success`:

A team is a standing conversation that people with Manage teams
(`chat.teams_manage`) set up: a name, people picked by hand and user
groups. Members of a user group are in its teams while they are in the
group, so a change in People takes effect at once. Every member with
Team chat reads and writes in it; only Manage teams changes or deletes
it, and its list shows the teams one is no member of too.

| Route | Permission | What |
|---|---|---|
| `GET /inbox?filter=open\|mine\|closed&q=` | chat.support | support conversations with their computer, newest message, unread count; `counts` |
| `GET /devices?q=` | chat.support | computers to start a conversation with |
| `GET /conversations/{id}` | either | a conversation with its computer (and `assignable` people) or its people |
| `POST /conversations/{id}/assign {account_id}` | chat.support and chat.assign | 0 gives it back to the inbox |
| `POST /conversations/{id}/close {note, resolve_help_request}` | chat.support; the assignee or chat.assign | |
| `POST /conversations/{id}/reopen`, `/link {ticket_id, help_request_id}` | chat.support | a computer's open help request is linked by itself when the computer writes |
| `GET /team` | chat.team | direct, group and team conversations, the colleagues one may write to, and for admins `old` |
| `POST /team/direct {account_id}`, `POST /team/groups {title, members}`, `PATCH /team/groups/{id}` | chat.team | |
| `GET /team/teams` | chat.team and chat.teams_manage | every team with its `people` (picked by hand) and `groups` (user group guids), the `user_groups` with their members, and the `people` a team can name |
| `POST /team/teams {title, people, groups}`, `PATCH /team/teams/{id}`, `DELETE /team/teams/{id}` | chat.team and chat.teams_manage | a list left out of a PATCH stays; DELETE removes the team with its messages |
| `GET /summary` | either | unread counts for the badges |
| `GET /search?q=&section=support\|team&conversation_id=` | either | up to 50 messages with the text (2 characters at least, case-insensitive for Latin letters), newest first, in the conversations the account reads, and `conversations` naming them by id |
| `GET/POST /canned`, `PATCH/DELETE /canned/{id}` | either; shared replies need chat.canned_manage | |
| `GET/PUT /me` | either | the account's notifications {browser, sound, mail} |
| `GET /settings`, `PUT /settings` | either; server.config to change | {mail_after_minutes, mail_preview, computers}; `computers` left out of a PUT keeps it |
| `GET /messages/{id}/trail` | audit.view | earlier texts of an edited or deleted message |
| `POST /upload`, `GET /files/{id}[?inline=1]` | either | files |
| `POST /device/files`, `GET /device/files/{id}` | device token | a computer's files (section 8) |
