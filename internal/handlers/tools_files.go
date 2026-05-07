package handlers

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
	"github.com/pwntato/notoriousmcp/internal/store"
)

func (h *Handler) handleListFiles(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	modifiedSince, rpcErr := parseModifiedSince(strArgOpt(args, "modified_since"))
	if rpcErr != nil {
		return nil, rpcErr
	}
	files, err := h.db.ListFiles(ctx, user.UserID, modifiedSince)
	if err != nil {
		return dbErrResult(err)
	}
	return jsonResult(files)
}

// cleanFilePath normalises a user-supplied file path: converts backslashes,
// applies path.Clean to remove traversal sequences, and strips the leading
// slash. Returns an error for paths that resolve to empty or ".".
func cleanFilePath(raw string) (string, error) {
	p := path.Clean("/" + strings.ReplaceAll(raw, `\`, "/"))
	p = strings.TrimPrefix(p, "/")
	if p == "" || p == "." {
		return "", fmt.Errorf("invalid path")
	}
	return p, nil
}

func (h *Handler) handleGetFile(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	raw, err := strArg(args, "path")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	filePath, err := cleanFilePath(raw)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	f, err := h.db.GetFile(ctx, user.UserID, filePath)
	if err != nil {
		return dbErrResult(err)
	}

	content, err := h.store.GetContent(ctx, f.S3Key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return errorResult("file content not found")
		}
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}
	f.Content = content
	return jsonResult(f)
}

func (h *Handler) handleSaveFile(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	rawPath, err := strArg(args, "path")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	filePath, err := cleanFilePath(rawPath)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	content, err := strArg(args, "content")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	now := time.Now().UTC()
	var f *models.File

	existing, dbErr := h.db.GetFile(ctx, user.UserID, filePath)
	if dbErr != nil && !errors.Is(dbErr, db.ErrNotFound) {
		return dbErrResult(dbErr)
	}

	if existing == nil {
		f = &models.File{
			Path:       filePath,
			UserID:     user.UserID,
			// Same stable-key-on-create trade-off as handleSaveNote; DB enforces
			// attribute_not_exists on Version==1 writes.
			S3Key:      fmt.Sprintf("files/%s/%s/%s", user.UserID, filePath, newID()),
			Size:       int64(len(content)),
			Version:    1,
			CreatedAt:  now,
			ModifiedAt: now,
		}
	} else {
		version := versionArg(args)
		if version == 0 {
			// version omitted: auto-increment bypasses optimistic concurrency.
			// Callers that need conflict detection must pass the current version.
			version = existing.Version + 1
		}
		f = &models.File{
			Path:       filePath,
			UserID:     user.UserID,
			// Fresh S3 key per write: same rationale as handleSaveNote.
			S3Key:      fmt.Sprintf("files/%s/%s/%s", user.UserID, filePath, newID()),
			Size:       int64(len(content)),
			Version:    version,
			CreatedAt:  existing.CreatedAt,
			ModifiedAt: now,
		}
	}

	// S3 write precedes DynamoDB write; on DB conflict the new S3 object is
	// orphaned but the previous version's object is untouched.
	if err := h.store.PutContent(ctx, f.S3Key, content); err != nil {
		if errors.Is(err, store.ErrTooLarge) {
			return errorResult("content exceeds 1MB limit")
		}
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}

	if err := h.db.SaveFile(ctx, f); err != nil {
		return dbErrResult(err)
	}

	return jsonResult(f)
}

func (h *Handler) handleDeleteFile(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	raw, err := strArg(args, "path")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	filePath, err := cleanFilePath(raw)
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	f, err := h.db.GetFile(ctx, user.UserID, filePath)
	if err != nil {
		return dbErrResult(err)
	}

	// DB-first: same rationale as handleDeleteNote.
	if err := h.db.DeleteFile(ctx, user.UserID, filePath); err != nil {
		return dbErrResult(err)
	}

	if err := h.store.DeleteContent(ctx, f.S3Key); err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}

	return textResult("file deleted")
}
