package handlers

import (
	"context"
	"time"

	"github.com/pwntato/notoriousmcp/internal/models"
)

func (h *Handler) handleListTodoLists(ctx context.Context, user *models.User, _ map[string]any) (*toolsCallResult, *rpcError) {
	lists, err := h.db.ListTodoLists(ctx, user.UserID)
	if err != nil {
		return dbErrResult(err)
	}
	return jsonResult(lists)
}

func (h *Handler) handleSaveTodoList(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	title, err := strArg(args, "title")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	tags := strSliceArgOpt(args, "tags")
	listID := strArgOpt(args, "list_id")

	now := time.Now().UTC()
	var list *models.TodoList

	if listID == "" {
		listID = newID()
		list = &models.TodoList{
			ID:         listID,
			UserID:     user.UserID,
			Title:      title,
			Tags:       tags,
			Version:    1,
			CreatedAt:  now,
			ModifiedAt: now,
		}
	} else {
		existing, err := h.db.GetTodoList(ctx, user.UserID, listID)
		if err != nil {
			return dbErrResult(err)
		}
		version := versionArg(args)
		if version == 0 {
			version = existing.Version + 1
		}
		list = &models.TodoList{
			ID:         listID,
			UserID:     user.UserID,
			Title:      title,
			Tags:       tags,
			Version:    version,
			CreatedAt:  existing.CreatedAt,
			ModifiedAt: now,
		}
	}

	if err := h.db.SaveTodoList(ctx, list); err != nil {
		return dbErrResult(err)
	}
	return jsonResult(list)
}

func (h *Handler) handleDeleteTodoList(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	listID, err := strArg(args, "list_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if err := h.db.DeleteTodoList(ctx, user.UserID, listID); err != nil {
		return dbErrResult(err)
	}
	return textResult("todo list deleted")
}

func (h *Handler) handleListTodos(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	listID, err := strArg(args, "list_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	modifiedSince := strArgOpt(args, "modified_since")
	statusStr := strArgOpt(args, "status")

	var statusFilter *models.TodoStatus
	if statusStr != "" {
		s := models.TodoStatus(statusStr)
		statusFilter = &s
	}

	todos, err := h.db.ListTodos(ctx, user.UserID, listID, modifiedSince, statusFilter)
	if err != nil {
		return dbErrResult(err)
	}
	return jsonResult(todos)
}

func (h *Handler) handleSaveTodo(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	listID, err := strArg(args, "list_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	text, err := strArg(args, "text")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	todoID := strArgOpt(args, "todo_id")
	statusStr := strArgOpt(args, "status")
	tags := strSliceArgOpt(args, "tags")
	dueDateStr := strArgOpt(args, "due_date")

	dueDate, parseErr := parseOptionalTime(dueDateStr)
	if parseErr != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: parseErr.Error()}
	}

	status := models.TodoPending
	if statusStr != "" {
		status = models.TodoStatus(statusStr)
	}

	now := time.Now().UTC()
	var todo *models.Todo

	if todoID == "" {
		todoID = newID()
		todo = &models.Todo{
			ID:         todoID,
			ListID:     listID,
			UserID:     user.UserID,
			Text:       text,
			Status:     status,
			DueDate:    dueDate,
			Tags:       tags,
			Version:    1,
			CreatedAt:  now,
			ModifiedAt: now,
		}
	} else {
		existing, err := h.db.GetTodo(ctx, user.UserID, listID, todoID)
		if err != nil {
			return dbErrResult(err)
		}
		version := versionArg(args)
		if version == 0 {
			version = existing.Version + 1
		}
		todo = &models.Todo{
			ID:         todoID,
			ListID:     listID,
			UserID:     user.UserID,
			Text:       text,
			Status:     status,
			DueDate:    dueDate,
			Tags:       tags,
			Version:    version,
			CreatedAt:  existing.CreatedAt,
			ModifiedAt: now,
		}
	}

	if err := h.db.SaveTodo(ctx, todo); err != nil {
		return dbErrResult(err)
	}
	return jsonResult(todo)
}

func (h *Handler) handleDeleteTodo(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	listID, err := strArg(args, "list_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	todoID, err := strArg(args, "todo_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if err := h.db.DeleteTodo(ctx, user.UserID, listID, todoID); err != nil {
		return dbErrResult(err)
	}
	return textResult("todo deleted")
}
