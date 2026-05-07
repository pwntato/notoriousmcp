package handlers

import (
	"context"

	"github.com/pwntato/notoriousmcp/internal/models"
)

// toolDef is the MCP tool definition returned by tools/list.
type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema schema `json:"inputSchema"`
}

type schema struct {
	Type       string              `json:"type"`
	Properties map[string]property `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

type property struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
	Items       *items   `json:"items,omitempty"`
}

type items struct {
	Type string `json:"type"`
}

// toolFn is the handler signature for a single tool call.
type toolFn func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError)

// registeredTool pairs the MCP definition with its handler. Using a single
// slice of registeredTool as the source of truth means the access-control gate
// in handleToolsCall and the dispatch are always in sync — adding a tool in one
// place automatically covers both.
type registeredTool struct {
	def toolDef
	fn  toolFn
}

// toolsForUser returns the registered tools appropriate for the given user's role.
func (h *Handler) toolsForUser(user *models.User) []registeredTool {
	switch user.Status {
	case models.StatusUser:
		return h.userTools()
	case models.StatusAdmin:
		return append(h.userTools(), h.adminTools()...)
	default:
		// pending or banned — check_status only
		return []registeredTool{h.statusTools()[0]}
	}
}

// toolDefsFor extracts the []toolDef slice from a []registeredTool, for tools/list.
func toolDefsFor(tools []registeredTool) []toolDef {
	defs := make([]toolDef, len(tools))
	for i, t := range tools {
		defs[i] = t.def
	}
	return defs
}


// statusTools returns the tool list for pending/banned users.
func (h *Handler) statusTools() []registeredTool {
	return []registeredTool{
		{
			def: toolDef{
				Name:        "check_status",
				Description: "Check your account status.",
				InputSchema: schema{Type: "object"},
			},
			fn: func(_ context.Context, user *models.User, _ map[string]any) (*toolsCallResult, *rpcError) {
				return handleCheckStatus(user)
			},
		},
	}
}

// userTools returns the 14 tools available to all active users.
func (h *Handler) userTools() []registeredTool {
	return []registeredTool{
		{
			def: toolDef{
				Name:        "search_notes",
				Description: "List note metadata, optionally filtered by modified-since timestamp.",
				InputSchema: schema{
					Type: "object",
					Properties: map[string]property{
						"modified_since": {Type: "string", Description: "ISO 8601 timestamp; only return notes modified after this time."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleSearchNotes(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "get_note",
				Description: "Get note metadata and full content by ID.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"note_id"},
					Properties: map[string]property{
						"note_id": {Type: "string", Description: "Note ID."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleGetNote(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "save_note",
				Description: "Create or update a note. Omit note_id to create. On update, version must match the current stored version.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"title", "content"},
					Properties: map[string]property{
						"note_id": {Type: "string", Description: "Note ID (omit to create)."},
						"title":   {Type: "string", Description: "Note title."},
						"content": {Type: "string", Description: "Note body (max 1MB)."},
						"tags":    {Type: "array", Description: "Tag list.", Items: &items{Type: "string"}},
						"version": {Type: "number", Description: "Current version for optimistic concurrency (omit when creating)."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleSaveNote(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "delete_note",
				Description: "Delete a note and its content.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"note_id"},
					Properties: map[string]property{
						"note_id": {Type: "string", Description: "Note ID."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleDeleteNote(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "list_todo_lists",
				Description: "List all todo lists.",
				InputSchema: schema{Type: "object"},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleListTodoLists(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "save_todo_list",
				Description: "Create or update a todo list. Omit list_id to create.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"title"},
					Properties: map[string]property{
						"list_id": {Type: "string", Description: "List ID (omit to create)."},
						"title":   {Type: "string", Description: "List title."},
						"tags":    {Type: "array", Description: "Tag list.", Items: &items{Type: "string"}},
						"version": {Type: "number", Description: "Current version for optimistic concurrency (omit when creating)."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleSaveTodoList(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "delete_todo_list",
				Description: "Delete a todo list. Does not cascade-delete its todos.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"list_id"},
					Properties: map[string]property{
						"list_id": {Type: "string", Description: "List ID."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleDeleteTodoList(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "list_todos",
				Description: "List todos in a list, optionally filtered by status or modified-since.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"list_id"},
					Properties: map[string]property{
						"list_id":        {Type: "string", Description: "List ID."},
						"status":         {Type: "string", Description: "Filter by status.", Enum: []string{"pending", "in_progress", "done"}},
						"modified_since": {Type: "string", Description: "ISO 8601 timestamp; only return todos modified after this time."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleListTodos(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "save_todo",
				Description: "Create or update a todo item. Omit todo_id to create.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"list_id", "text"},
					Properties: map[string]property{
						"list_id":  {Type: "string", Description: "List ID."},
						"todo_id":  {Type: "string", Description: "Todo ID (omit to create)."},
						"text":     {Type: "string", Description: "Todo item text."},
						"status":   {Type: "string", Description: "Todo status.", Enum: []string{"pending", "in_progress", "done"}},
						"due_date": {Type: "string", Description: "Due date (RFC3339)."},
						"tags":     {Type: "array", Description: "Tag list.", Items: &items{Type: "string"}},
						"version":  {Type: "number", Description: "Current version for optimistic concurrency (omit when creating)."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleSaveTodo(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "delete_todo",
				Description: "Delete a todo item.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"list_id", "todo_id"},
					Properties: map[string]property{
						"list_id": {Type: "string", Description: "List ID."},
						"todo_id": {Type: "string", Description: "Todo ID."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleDeleteTodo(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "list_files",
				Description: "List file metadata, optionally filtered by modified-since timestamp.",
				InputSchema: schema{
					Type: "object",
					Properties: map[string]property{
						"modified_since": {Type: "string", Description: "ISO 8601 timestamp; only return files modified after this time."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleListFiles(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "get_file",
				Description: "Get file metadata and full content by path.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"path"},
					Properties: map[string]property{
						"path": {Type: "string", Description: "File path."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleGetFile(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "save_file",
				Description: "Create or update a file. On update, version must match the current stored version.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"path", "content"},
					Properties: map[string]property{
						"path":    {Type: "string", Description: "File path."},
						"content": {Type: "string", Description: "File content (max 1MB)."},
						"version": {Type: "number", Description: "Current version for optimistic concurrency (omit when creating)."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleSaveFile(ctx, user, args)
			},
		},
		{
			def: toolDef{
				Name:        "delete_file",
				Description: "Delete a file and its content.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"path"},
					Properties: map[string]property{
						"path": {Type: "string", Description: "File path."},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleDeleteFile(ctx, user, args)
			},
		},
	}
}

// adminTools returns the 2 tools available only to admins.
func (h *Handler) adminTools() []registeredTool {
	return []registeredTool{
		{
			def: toolDef{
				Name:        "list_users",
				Description: "List users, optionally filtered by status.",
				InputSchema: schema{
					Type: "object",
					Properties: map[string]property{
						"status": {Type: "string", Description: "Filter by status.", Enum: []string{"pending", "user", "admin", "banned"}},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleListUsers(ctx, args)
			},
		},
		{
			def: toolDef{
				Name:        "update_user",
				Description: "Update a user's status.",
				InputSchema: schema{
					Type:     "object",
					Required: []string{"user_id", "status"},
					Properties: map[string]property{
						"user_id": {Type: "string", Description: "User ID."},
						"status":  {Type: "string", Description: "New status.", Enum: []string{"pending", "user", "admin", "banned"}},
					},
				},
			},
			fn: func(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
				return h.handleUpdateUser(ctx, args)
			},
		},
	}
}

