# NotoriousMCP Skill

NotoriousMCP is a self-hosted MCP server for notes, todo lists, files, and LLM memory — backed by AWS Lambda + DynamoDB + S3. This skill teaches you when and how to use its tools effectively.

## Installation

Add to `.claude/settings.json` (or the global `~/.claude/settings.json`):

```json
{
  "skills": ["path/to/notoriousmcp/skill/notoriousmcp.md"]
}
```

Or copy it to wherever your project's skill files live and reference that path.

## Auth

The server uses OAuth 2.0 (Google or Okta). Access tokens are 1-hour HMAC tokens passed as `Authorization: Bearer <token>`. When a token expires, the server silently issues a fresh one via the `X-New-Token` response header — your MCP client handles this automatically.

New accounts start as `pending` and only have access to `check_status`. An admin must approve you before the full tool set is available.

## Session Start

At the start of each session, load any persistent memory you've stored. A recommended layout:

```
memory/MEMORY.md      # index: topic → file path
memory/user.md
memory/project_foo.md
memory/feedback.md
```

Fetch the index first, then individual files as needed:

```
get_file("memory/MEMORY.md")
get_file("memory/<topic>.md")
```

If `memory/MEMORY.md` doesn't exist yet, skip silently — the user hasn't set up server-side memory yet. This mirrors the local `~/.claude/` memory system but stores data server-side, available across machines.

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
- On update, pass `version` set to the **current version + 1** (i.e. the version you want to store). The server checks that the stored version equals `version - 1`. Omit `version` to auto-increment (skips conflict detection).
- Content limit: 1MB.

### Todo Lists (user+)

```
list_todo_lists()                                       → []TodoList
save_todo_list(title, list_id?, tags?, version?)        → TodoList
delete_todo_list(list_id)
```

- `delete_todo_list` does not cascade-delete todos. Orphaned todos remain in DynamoDB and are still reachable via `list_todos(list_id=<deleted_id>)`. Delete the todos first via `delete_todo` if you want a clean removal.

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
- `user_id` is the provider subject ID (`sub`) — an opaque string returned in the `user_id` field of `list_users` output.
- Admins cannot change their own status away from `admin`.

### Status (pending/banned)

```
check_status()    → account status message
```

## Conflict-Safe Writes

Every saved item has a `version` integer. To update safely:

1. Read the item (note, file, todo, etc.) — the response includes `version`.
2. Pass `version + 1` in your `save_*` call (the version you want to store, not the one you read).
3. If another writer changed it in the meantime, the tool result will have `isError: true` with the text `"version conflict: reload and retry"` — re-read the item and retry.

Example: read returns `"version": 3` → pass `"version": 4` in the update.

Omitting `version` on an update silently auto-increments and skips conflict detection. This is fine for single-writer sessions; use explicit `version` when multiple clients or sessions may write the same item.
