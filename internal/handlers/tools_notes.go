package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pwntato/notoriousmcp/internal/models"
	"github.com/pwntato/notoriousmcp/internal/store"
)

func (h *Handler) handleSearchNotes(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	modifiedSince := strArgOpt(args, "modified_since")
	notes, err := h.db.ListNotes(ctx, user.UserID, modifiedSince)
	if err != nil {
		return dbErrResult(err)
	}
	return jsonResult(notes)
}

func (h *Handler) handleGetNote(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	noteID, err := strArg(args, "note_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	note, err := h.db.GetNote(ctx, user.UserID, noteID)
	if err != nil {
		return dbErrResult(err)
	}

	content, err := h.store.GetContent(ctx, note.S3Key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errorResult("note content not found")
		}
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}
	note.Content = content
	return jsonResult(note)
}

func (h *Handler) handleSaveNote(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	title, err := strArg(args, "title")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	content, err := strArg(args, "content")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	tags := strSliceArgOpt(args, "tags")
	noteID := strArgOpt(args, "note_id")

	now := time.Now().UTC()
	var note *models.Note

	if noteID == "" {
		noteID = newID()
		note = &models.Note{
			ID:         noteID,
			UserID:     user.UserID,
			Title:      title,
			Tags:       tags,
			S3Key:      fmt.Sprintf("notes/%s/%s", user.UserID, noteID),
			Version:    1,
			CreatedAt:  now,
			ModifiedAt: now,
		}
	} else {
		existing, err := h.db.GetNote(ctx, user.UserID, noteID)
		if err != nil {
			return dbErrResult(err)
		}
		version := versionArg(args)
		if version == 0 {
			// version omitted: auto-increment bypasses optimistic concurrency.
			// Callers that need conflict detection must pass the current version.
			version = existing.Version + 1
		}
		note = &models.Note{
			ID:         noteID,
			UserID:     user.UserID,
			Title:      title,
			Tags:       tags,
			S3Key:      existing.S3Key,
			Version:    version,
			CreatedAt:  existing.CreatedAt,
			ModifiedAt: now,
		}
	}

	// S3 write precedes DynamoDB write; if the DB write fails (e.g. version
	// conflict) the S3 object becomes orphaned. Acceptable at current scale —
	// tracked for future cleanup via a lifecycle policy or explicit rollback.
	if err := h.store.PutContent(ctx, note.S3Key, content); err != nil {
		if errors.Is(err, store.ErrTooLarge) {
			return errorResult("content exceeds 1MB limit")
		}
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}

	if err := h.db.SaveNote(ctx, note); err != nil {
		return dbErrResult(err)
	}

	return jsonResult(note)
}

func (h *Handler) handleDeleteNote(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	noteID, err := strArg(args, "note_id")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	note, err := h.db.GetNote(ctx, user.UserID, noteID)
	if err != nil {
		return dbErrResult(err)
	}

	// DB-first: if the S3 delete later fails, the DB record is already gone so
	// subsequent reads return not-found rather than a dangling pointer. The
	// orphaned S3 object is recoverable; a dangling DB reference is harder to
	// detect and correct.
	if err := h.db.DeleteNote(ctx, user.UserID, noteID); err != nil {
		return dbErrResult(err)
	}

	if err := h.store.DeleteContent(ctx, note.S3Key); err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}

	return textResult("note deleted")
}

