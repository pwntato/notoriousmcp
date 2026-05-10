package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
)

// userWithUsage wraps a User with computed usage percentages for the admin
// list_users response. This is the only path where User is serialized to JSON;
// both list_users and update_user are admin-only tools.
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
	out := make([]userWithUsage, len(users))
	errc := make(chan error, len(users))

	// Fetch per-user transfer totals concurrently — one GetItem per user would
	// be slow in serial for large user lists.
	// TODO: replace with BatchGetItem once user counts grow enough to matter.
	var wg sync.WaitGroup
	for i := range users {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := &users[i]
			transferUsedBefore, err := h.db.GetTransferUsed(ctx, u.UserID, month)
			if err != nil {
				errc <- fmt.Errorf("get transfer for %s: %w", u.UserID, err)
				return
			}
			storageCap := h.effectiveStorageCap(u)
			transferCap := h.effectiveTransferCap(u)
			storagePct := 0
			if storageCap > 0 && u.StorageUsedBytes > 0 {
				// Round up to 1 so any non-zero usage shows as at least 1%.
				// Capped at 100 — over-quota is already blocked at write time.
				storagePct = min(100, max(1, int(float64(u.StorageUsedBytes)*100/float64(storageCap))))
			}
			transferPct := 0
			if transferCap > 0 && transferUsedBefore > 0 {
				transferPct = min(100, max(1, int(float64(transferUsedBefore)*100/float64(transferCap))))
			}
			out[i] = userWithUsage{
				User:            *u,
				StorageUsedPct:  storagePct,
				TransferUsedPct: transferPct,
			}
		}(i)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		log.Printf("admin: list users: %v", err)
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}
	return jsonResult(out)
}

func (h *Handler) handleUpdateUser(ctx context.Context, caller *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	userID, err := strArg(args, "user_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	// Validate that at least one field is being changed before touching the DB.
	statusStr := strArgOpt(args, "status")
	_, hasStorage := int64ArgOpt(args, "storage_cap_bytes")
	_, hasTransfer := int64ArgOpt(args, "transfer_cap_bytes")
	if statusStr == "" && !hasStorage && !hasTransfer {
		return nil, &rpcError{Code: codeInvalidParams, Message: "at least one of status, storage_cap_bytes, or transfer_cap_bytes must be provided"}
	}

	// status is optional — omit when only updating caps.
	if statusStr != "" {
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
	}

	// Optional cap overrides. Each field is independent: omit to leave unchanged,
	// pass -1 to clear the per-user override and restore the server default.
	if storageCap, ok := int64ArgOpt(args, "storage_cap_bytes"); ok {
		var capPtr *int64
		if storageCap >= 0 {
			capPtr = &storageCap
		}
		if err := h.db.UpdateStorageCap(ctx, userID, capPtr); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return errorResult("user not found")
			}
			log.Printf("admin: update storage cap for %s: %v", userID, err)
			return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
		}
	}
	if transferCap, ok := int64ArgOpt(args, "transfer_cap_bytes"); ok {
		var capPtr *int64
		if transferCap >= 0 {
			capPtr = &transferCap
		}
		if err := h.db.UpdateTransferCap(ctx, userID, capPtr); err != nil {
			if errors.Is(err, db.ErrNotFound) {
				return errorResult("user not found")
			}
			log.Printf("admin: update transfer cap for %s: %v", userID, err)
			return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
		}
	}

	return textResult("user updated")
}
