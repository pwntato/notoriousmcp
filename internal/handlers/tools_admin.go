package handlers

import (
	"context"
	"errors"
	"log"

	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
)

// userWithUsage wraps a User and adds computed usage percentage fields for admins.
type userWithUsage struct {
	models.User
	StorageUsedPct  int `json:"storage_used_pct"`
	TransferUsedPct int `json:"transfer_used_pct"`
}

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

	month := currentMonth()
	out := make([]userWithUsage, 0, len(users))
	for _, u := range users {
		transferUsed, err := h.db.GetTransferUsed(ctx, u.UserID, month)
		if err != nil {
			log.Printf("admin: list users: get transfer for %s: %v", u.UserID, err)
		}
		storageCap := h.effectiveStorageCap(&u)
		transferCap := h.effectiveTransferCap(&u)
		storagePct := 0
		if storageCap > 0 {
			storagePct = int(u.StorageUsedBytes * 100 / storageCap)
		}
		transferPct := 0
		if transferCap > 0 {
			transferPct = int(transferUsed * 100 / transferCap)
		}
		out = append(out, userWithUsage{
			User:            u,
			StorageUsedPct:  storagePct,
			TransferUsedPct: transferPct,
		})
	}
	return jsonResult(out)
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

	// Optional cap overrides. Both are nullable: pass -1 to clear, omit to leave unchanged.
	storageCap, hasStorage := int64ArgOpt(args, "storage_cap_bytes")
	transferCap, hasTransfer := int64ArgOpt(args, "transfer_cap_bytes")
	if hasStorage || hasTransfer {
		var scPtr, tcPtr *int64
		if hasStorage && storageCap >= 0 {
			scPtr = &storageCap
		}
		if hasTransfer && transferCap >= 0 {
			tcPtr = &transferCap
		}
		if err := h.db.UpdateUserCaps(ctx, userID, scPtr, tcPtr); err != nil {
			if !errors.Is(err, db.ErrNotFound) {
				log.Printf("admin: update caps for %s: %v", userID, err)
			}
		}
	}

	log.Printf("admin: user %s status set to %s", userID, status)
	return textResult("user updated")
}
