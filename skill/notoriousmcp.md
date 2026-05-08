# NoteoriousMCP Skill

NoteoriousMCP is a self-hosted MCP server for notes, todo lists, files, and LLM memory — backed by AWS Lambda + DynamoDB + S3. This skill teaches you when and how to use its tools effectively.

## Installation

```bash
cp skill/notoriousmcp.md .claude/skills/notoriousmcp.md
```

Or add to Claude Code settings under `skills`.

## Auth

The server uses Google OAuth 2.0. Access tokens are 1-hour HMAC tokens passed as `Authorization: Bearer <token>`. When a token expires, the server silently issues a fresh one via the `X-New-Token` response header — your MCP client handles this automatically.

New accounts start as `pending` and only have access to `check_status`. An admin must approve you before the full tool set is available.

## Session Start

At the start of each session, load any persistent memory you've stored:

```
get_file("memory/MEMORY.md")       # index, then fetch individual files as needed
get_file("memory/<topic>.md")
```

If `memory/MEMORY.md` doesn't exist yet, skip silently — the user hasn't set up server-side memory yet.

## Session End

Before ending a session where you learned or changed something worth remembering, save updated memory files:

```
save_file(path="memory/MEMORY.md", content="...", version=<current_version>)
```

Always pass `version` when updating an existing file to avoid overwriting concurrent changes. Omit `version` only when creating a new file.

## Tool Reference

### Notes (user+)

```
search_notes(modified_since?)                           → []Note metadata
get_note(note_id)                                       → Note + content
save_note(title, content, note_id?, tags?, version?)    → Note
delete_note(note_id)
```

- `search_notes` returns metadata only (no content). Use `get_note` to fetch content.
- Pass `modified_since` (ISO 8601) to fetch only recently changed notes — useful for sync.
- On update, pass `version` matching the current stored version for conflict-safe writes. Omitting `version` auto-increments and skips conflict detection.
- Content limit: 1MB.

### Todo Lists (user+)

```
list_todo_lists()                                       → []TodoList
save_todo_list(title, list_id?, tags?, version?)        → TodoList
delete_todo_list(list_id)                               # does not cascade-delete todos
```

### Todos (user+)

```
list_todos(list_id, status?, modified_since?)           → []Todo
save_todo(list_id, text, todo_id?, status?, due_date?, tags?, version?)  → Todo
delete_todo(list_id, todo_id)
```

- `status` enum: `pending` | `in_progress` | `done`
- `due_date`: RFC3339 string
- Use `status` filter to fetch only active items (e.g. `status="pending"` or `status="in_progress"`).
- Use `modified_since` to sync changes since a known timestamp.

### Files (user+)

```
list_files(modified_since?)                             → []File metadata
get_file(path)                                          → File + content
save_file(path, content, version?)                      → File
delete_file(path)
```

- Paths are slash-separated strings (e.g. `memory/MEMORY.md`). Traversal sequences are stripped server-side.
- Content limit: 1MB.
- Use the `files/` namespace for arbitrary structured data: memory indexes, config snapshots, exported notes.

### Admin only

```
list_users(status?)                                     → []User
update_user(user_id, status)
```

- `status` enum for users: `pending` | `user` | `admin` | `banned`
- Admins cannot change their own status away from `admin`.

### Status (pending/banned)

```
check_status()    → account status message
```

## Memory Pattern

Use `save_file` / `get_file` to persist Claude Code memory on the server. A simple layout:

```
memory/MEMORY.md          # index of topics → file paths
memory/user.md
memory/project_foo.md
memory/feedback.md
```

Load `memory/MEMORY.md` at session start, then fetch individual files as needed. Save changes before ending the session. This mirrors the local `~/.claude/` memory system but stores data server-side, shared across machines.

## Conflict-Safe Writes

Every saved item has a `version` integer. To update safely:

1. Read the item (note, file, todo, etc.) — the response includes `version`.
2. Pass that `version` back in your `save_*` call.
3. If another writer changed it in the meantime, the server returns a version conflict error — re-read and retry.

Omitting `version` on an update silently auto-increments and skips conflict detection. This is fine for single-writer sessions; use explicit `version` when multiple clients or sessions may write the same item.
