package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
	"github.com/pwntato/notoriousmcp/internal/store"
)

func (h *Handler) handleSearchNotes(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	modifiedSince, rpcErr := parseModifiedSince(strArgOpt(args, "modified_since"))
	if rpcErr != nil {
		return nil, rpcErr
	}
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
	result, rpcErr := jsonResult(note)
	if rpcErr != nil {
		return nil, rpcErr
	}

	// Check and record transfer using the actual response size so both sides
	// of the enforcement use the same unit.
	responseBytes := int64(len([]byte(result.Content[0].Text)))
	transferUsed, rpcErr := h.checkTransferCap(ctx, user)
	if rpcErr != nil {
		return nil, rpcErr
	}
	if transferUsed+responseBytes > h.effectiveTransferCap(user) {
		return dbErrResult(db.ErrTransferCap)
	}
	if _, err := h.db.AddTransferUsed(ctx, user.UserID, currentMonth(), responseBytes, transferTTL()); err != nil {
		log.Printf("mcp: get note %s: record transfer: %v", noteID, err)
	}

	return result, nil
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
	newSize := int64(len([]byte(content)))
	var note *models.Note

	if noteID == "" {
		// Create path. user.StorageUsedBytes is from auth middleware (request time)
		// so concurrent saves can both pass this check; soft enforcement accepted.
		if user.StorageUsedBytes+newSize > h.effectiveStorageCap(user) {
			return dbErrResult(db.ErrStorageCap)
		}

		noteID = newID()
		note = &models.Note{
			ID:     noteID,
			UserID: user.UserID,
			Title:  title,
			Tags:   tags,
			// Create path uses a stable key; updates use a fresh key per write.
			// A failure between the S3 write and the first DB update (v1→v2)
			// would overwrite this object, but that window is small and the
			// orphan-on-conflict strategy applies from the second write onward.
			// The DB enforces attribute_not_exists on Version==1 writes, so
			// concurrent creates for the same ID safely conflict at the DB layer.
			S3Key:      fmt.Sprintf("notes/%s/%s", user.UserID, noteID),
			Size:       newSize,
			Version:    1,
			CreatedAt:  now,
			ModifiedAt: now,
		}

		// Create path: stable S3 key, no old object to clean up.
		if err := h.store.PutContent(ctx, note.S3Key, content); err != nil {
			if errors.Is(err, store.ErrTooLarge) {
				return errorResult("content exceeds 1MB limit")
			}
			return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
		}

		if err := h.db.SaveNote(ctx, note); err != nil {
			return dbErrResult(err)
		}

		if err := h.db.AddStorageUsed(ctx, user.UserID, newSize); err != nil {
			log.Printf("mcp: save note %s: update storage used: %v", noteID, err)
		}

		return jsonResult(note)
	}

	// Update path.
	existing, err := h.db.GetNote(ctx, user.UserID, noteID)
	if err != nil {
		return dbErrResult(err)
	}

	oldSize := existing.Size
	delta := newSize - oldSize
	if delta > 0 && user.StorageUsedBytes+delta > h.effectiveStorageCap(user) {
		return dbErrResult(db.ErrStorageCap)
	}

	version := versionArg(args)
	if version == 0 {
		// version omitted: auto-increment bypasses optimistic concurrency.
		// Callers that need conflict detection must pass the current version.
		version = existing.Version + 1
	}
	note = &models.Note{
		ID:     noteID,
		UserID: user.UserID,
		Title:  title,
		Tags:   tags,
		// Fresh S3 key per write: if the DB write fails (version conflict),
		// the new S3 object is orphaned but the DB record still points to the
		// previous key, so prior content is never overwritten.
		S3Key:      fmt.Sprintf("notes/%s/%s/%s", user.UserID, noteID, newID()),
		Size:       newSize,
		Version:    version,
		CreatedAt:  existing.CreatedAt,
		ModifiedAt: now,
	}

	// S3 write precedes DynamoDB write; on DB conflict the new S3 object is
	// orphaned but the previous version's object is untouched (version-stamped
	// keys ensure writes never overwrite earlier content).
	if err := h.store.PutContent(ctx, note.S3Key, content); err != nil {
		if errors.Is(err, store.ErrTooLarge) {
			return errorResult("content exceeds 1MB limit")
		}
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}

	if err := h.db.SaveNote(ctx, note); err != nil {
		return dbErrResult(err)
	}

	// DB write succeeded; delete the now-unreferenced previous S3 object.
	// Log on failure but don't surface the error — the save already succeeded.
	if err := h.store.DeleteContent(ctx, existing.S3Key); err != nil {
		log.Printf("mcp: save note %s: cleanup old s3 key %s: %v", noteID, existing.S3Key, err)
	}

	if err := h.db.AddStorageUsed(ctx, user.UserID, delta); err != nil {
		log.Printf("mcp: save note %s: update storage used: %v", noteID, err)
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

	// Returning an error here even though the DB record is gone is intentional:
	// the S3 failure is real and the client should know. A retry will hit
	// ErrNotFound on the DB read and return "not found" gracefully.
	if err := h.store.DeleteContent(ctx, note.S3Key); err != nil {
		log.Printf("mcp: delete note %s: s3 delete %s: %v", noteID, note.S3Key, err)
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}

	if err := h.db.AddStorageUsed(ctx, user.UserID, -note.Size); err != nil {
		log.Printf("mcp: delete note %s: update storage used: %v", noteID, err)
	}

	return textResult("note deleted")
}
