package handlers

import (
	"context"
	"errors"
	"log"

	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
)

func (h *Handler) handleListUsers(ctx context.Context, args map[string]any) (*toolsCallResult, *rpcError) {
	statusStr := strArgOpt(args, "status")
	var statusFilter *models.UserStatus
	if statusStr != "" {
		s := models.UserStatus(statusStr)
		switch s {
		case models.StatusPending, models.StatusUser, models.StatusAdmin, models.StatusBanned:
		default:
			return nil, &rpcError{Code: codeInvalidParams, Message: "invalid status value"}
		}
		statusFilter = &s
	}
	users, err := h.db.ListUsers(ctx, statusFilter)
	if err != nil {
		return dbErrResult(err)
	}
	return jsonResult(users)
}

func (h *Handler) handleUpdateUser(ctx context.Context, caller *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	userID, err := strArg(args, "user_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	statusStr, err := strArg(args, "status")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	status := models.UserStatus(statusStr)
	switch status {
	case models.StatusPending, models.StatusUser, models.StatusAdmin, models.StatusBanned:
	default:
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid status value"}
	}

	if userID == caller.UserID && status != models.StatusAdmin {
		return errorResult("admins cannot change their own status")
	}

	if err := h.db.UpdateUserStatus(ctx, userID, status); err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return errorResult("user not found")
		}
		return dbErrResult(err)
	}
	log.Printf("admin: user %s status set to %s", userID, status)
	return textResult("user updated")
}
