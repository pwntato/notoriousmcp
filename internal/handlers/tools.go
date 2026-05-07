package handlers

import (
	"context"
	"fmt"

	"github.com/pwntato/notoriousmcp/internal/models"
)

// toolDef is the MCP tool definition returned by tools/list.
type toolDef struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	InputSchema schema   `json:"inputSchema"`
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

var (
	toolCheckStatus = toolDef{
		Name:        "check_status",
		Description: "Check your account status.",
		InputSchema: schema{Type: "object"},
	}

	toolSearchNotes = toolDef{
		Name:        "search_notes",
		Description: "List note metadata, optionally filtered by modified-since timestamp.",
		InputSchema: schema{
			Type: "object",
			Properties: map[string]property{
				"modified_since": {Type: "string", Description: "ISO 8601 timestamp; only return notes modified after this time."},
			},
		},
	}

	toolGetNote = toolDef{
		Name:        "get_note",
		Description: "Get note metadata and full content by ID.",
		InputSchema: schema{
			Type:     "object",
			Required: []string{"note_id"},
			Properties: map[string]property{
				"note_id": {Type: "string", Description: "Note ID."},
			},
		},
	}

	toolSaveNote = toolDef{
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
	}

	toolDeleteNote = toolDef{
		Name:        "delete_note",
		Description: "Delete a note and its content.",
		InputSchema: schema{
			Type:     "object",
			Required: []string{"note_id"},
			Properties: map[string]property{
				"note_id": {Type: "string", Description: "Note ID."},
			},
		},
	}

	toolListTodoLists = toolDef{
		Name:        "list_todo_lists",
		Description: "List all todo lists.",
		InputSchema: schema{Type: "object"},
	}

	toolSaveTodoList = toolDef{
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
	}

	toolDeleteTodoList = toolDef{
		Name:        "delete_todo_list",
		Description: "Delete a todo list. Does not cascade-delete its todos.",
		InputSchema: schema{
			Type:     "object",
			Required: []string{"list_id"},
			Properties: map[string]property{
				"list_id": {Type: "string", Description: "List ID."},
			},
		},
	}

	toolListTodos = toolDef{
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
	}

	toolSaveTodo = toolDef{
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
	}

	toolDeleteTodo = toolDef{
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
	}

	toolListFiles = toolDef{
		Name:        "list_files",
		Description: "List file metadata, optionally filtered by modified-since timestamp.",
		InputSchema: schema{
			Type: "object",
			Properties: map[string]property{
				"modified_since": {Type: "string", Description: "ISO 8601 timestamp; only return files modified after this time."},
			},
		},
	}

	toolGetFile = toolDef{
		Name:        "get_file",
		Description: "Get file metadata and full content by path.",
		InputSchema: schema{
			Type:     "object",
			Required: []string{"path"},
			Properties: map[string]property{
				"path": {Type: "string", Description: "File path."},
			},
		},
	}

	toolSaveFile = toolDef{
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
	}

	toolDeleteFile = toolDef{
		Name:        "delete_file",
		Description: "Delete a file and its content.",
		InputSchema: schema{
			Type:     "object",
			Required: []string{"path"},
			Properties: map[string]property{
				"path": {Type: "string", Description: "File path."},
			},
		},
	}

	toolListUsers = toolDef{
		Name:        "list_users",
		Description: "List users, optionally filtered by status.",
		InputSchema: schema{
			Type: "object",
			Properties: map[string]property{
				"status": {Type: "string", Description: "Filter by status.", Enum: []string{"pending", "user", "admin", "banned"}},
			},
		},
	}

	toolUpdateUser = toolDef{
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
	}
)

// userTools are the 13 tools available to all active users.
var userTools = []toolDef{
	toolSearchNotes,
	toolGetNote,
	toolSaveNote,
	toolDeleteNote,
	toolListTodoLists,
	toolSaveTodoList,
	toolDeleteTodoList,
	toolListTodos,
	toolSaveTodo,
	toolDeleteTodo,
	toolListFiles,
	toolGetFile,
	toolSaveFile,
	toolDeleteFile,
}

// adminOnlyTools are the 2 additional tools available to admins.
var adminOnlyTools = []toolDef{
	toolListUsers,
	toolUpdateUser,
}

// toolsForUser returns the tool list appropriate for the given user's role.
func toolsForUser(user *models.User) []toolDef {
	switch user.Status {
	case models.StatusUser:
		return userTools
	case models.StatusAdmin:
		tools := make([]toolDef, len(userTools)+len(adminOnlyTools))
		copy(tools, userTools)
		copy(tools[len(userTools):], adminOnlyTools)
		return tools
	default:
		// pending or banned — check_status only
		return []toolDef{toolCheckStatus}
	}
}

// callTool dispatches to the appropriate tool handler.
func (h *Handler) callTool(ctx context.Context, user *models.User, name string, args map[string]any) (*toolsCallResult, *rpcError) {
	switch name {
	case "check_status":
		return handleCheckStatus(user)
	case "search_notes":
		return h.handleSearchNotes(ctx, user, args)
	case "get_note":
		return h.handleGetNote(ctx, user, args)
	case "save_note":
		return h.handleSaveNote(ctx, user, args)
	case "delete_note":
		return h.handleDeleteNote(ctx, user, args)
	case "list_todo_lists":
		return h.handleListTodoLists(ctx, user, args)
	case "save_todo_list":
		return h.handleSaveTodoList(ctx, user, args)
	case "delete_todo_list":
		return h.handleDeleteTodoList(ctx, user, args)
	case "list_todos":
		return h.handleListTodos(ctx, user, args)
	case "save_todo":
		return h.handleSaveTodo(ctx, user, args)
	case "delete_todo":
		return h.handleDeleteTodo(ctx, user, args)
	case "list_files":
		return h.handleListFiles(ctx, user, args)
	case "get_file":
		return h.handleGetFile(ctx, user, args)
	case "save_file":
		return h.handleSaveFile(ctx, user, args)
	case "delete_file":
		return h.handleDeleteFile(ctx, user, args)
	case "list_users":
		return h.handleListUsers(ctx, args)
	case "update_user":
		return h.handleUpdateUser(ctx, args)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("unknown tool: %s", name)}
	}
}
