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
	modifiedSince := strArgOpt(args, "modified_since")
	files, err := h.db.ListFiles(ctx, user.UserID, modifiedSince)
	if err != nil {
		return dbErrResult(err)
	}
	return jsonResult(files)
}

func (h *Handler) handleGetFile(ctx context.Context, user *models.User, args map[string]any) (*toolsCallResult, *rpcError) {
	filePath, err := strArg(args, "path")
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
	// Normalise the path to remove traversal sequences (e.g. "../../foo").
	// DynamoDB enforces data isolation via PK=USER#<id>, so a crafted path
	// cannot read another user's data, but cleaning prevents unexpected S3 keys.
	filePath := path.Clean("/" + strings.ReplaceAll(rawPath, `\`, "/"))
	filePath = strings.TrimPrefix(filePath, "/")
	if filePath == "" || filePath == "." {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid path"}
	}

	content, err := strArg(args, "content")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	now := time.Now().UTC()
	s3Key := fmt.Sprintf("files/%s/%s", user.UserID, filePath)
	var f *models.File

	existing, dbErr := h.db.GetFile(ctx, user.UserID, filePath)
	if dbErr != nil && !errors.Is(dbErr, db.ErrNotFound) {
		return dbErrResult(dbErr)
	}

	if existing == nil {
		f = &models.File{
			Path:       filePath,
			UserID:     user.UserID,
			S3Key:      s3Key,
			Size:       int64(len(content)),
			Version:    1,
			CreatedAt:  now,
			ModifiedAt: now,
		}
	} else {
		version := versionArg(args)
		if version == 0 {
			version = existing.Version + 1
		}
		f = &models.File{
			Path:       filePath,
			UserID:     user.UserID,
			S3Key:      existing.S3Key,
			Size:       int64(len(content)),
			Version:    version,
			CreatedAt:  existing.CreatedAt,
			ModifiedAt: now,
		}
	}

	// S3 write precedes DynamoDB write; if the DB write fails (e.g. version
	// conflict) the S3 object becomes orphaned. Same trade-off as handleSaveNote.
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
	filePath, err := strArg(args, "path")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	f, err := h.db.GetFile(ctx, user.UserID, filePath)
	if err != nil {
		return dbErrResult(err)
	}

	if err := h.store.DeleteContent(ctx, f.S3Key); err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}

	if err := h.db.DeleteFile(ctx, user.UserID, filePath); err != nil {
		return dbErrResult(err)
	}

	return textResult("file deleted")
}
