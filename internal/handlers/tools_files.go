package handlers

import (
	"context"
	"errors"
	"fmt"
	"time"

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
	path, err := strArg(args, "path")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	f, err := h.db.GetFile(ctx, user.UserID, path)
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
	path, err := strArg(args, "path")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	content, err := strArg(args, "content")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	now := time.Now().UTC()
	s3Key := fmt.Sprintf("files/%s/%s", user.UserID, path)
	var f *models.File

	existing, dbErr := h.db.GetFile(ctx, user.UserID, path)
	if dbErr != nil && !isNotFound(dbErr) {
		return dbErrResult(dbErr)
	}

	if existing == nil {
		f = &models.File{
			Path:       path,
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
			Path:       path,
			UserID:     user.UserID,
			S3Key:      existing.S3Key,
			Size:       int64(len(content)),
			Version:    version,
			CreatedAt:  existing.CreatedAt,
			ModifiedAt: now,
		}
	}

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
	path, err := strArg(args, "path")
	if err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}

	f, err := h.db.GetFile(ctx, user.UserID, path)
	if err != nil {
		return dbErrResult(err)
	}

	if err := h.store.DeleteContent(ctx, f.S3Key); err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}

	if err := h.db.DeleteFile(ctx, user.UserID, path); err != nil {
		return dbErrResult(err)
	}

	return textResult("file deleted")
}
