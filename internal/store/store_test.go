package store_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"

	"github.com/pwntato/notoriousmcp/internal/store"
)

func uid() string { return fmt.Sprintf("%x", rand.Uint64()) }

func newTestClient(t *testing.T) *store.Client {
	t.Helper()
	endpoint := os.Getenv("S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("S3_ENDPOINT not set; skipping integration test")
	}
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		bucket = "notoriousmcp-local"
	}
	c, err := store.New(context.Background(), bucket, endpoint)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestPutAndGet(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	key := "test/" + uid()
	content := "hello, world"

	if err := c.PutContent(ctx, key, content); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := c.GetContent(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != content {
		t.Errorf("got %q want %q", got, content)
	}
}

func TestGetNotFound(t *testing.T) {
	c := newTestClient(t)
	_, err := c.GetContent(context.Background(), "nonexistent/"+uid())
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	key := "test/" + uid()
	if err := c.PutContent(ctx, key, "to be deleted"); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := c.DeleteContent(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := c.GetContent(ctx, key)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDeleteIdempotent(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Deleting a non-existent key must not error
	if err := c.DeleteContent(ctx, "nonexistent/"+uid()); err != nil {
		t.Errorf("delete non-existent: expected nil, got %v", err)
	}
}

func TestPutOverwrites(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	key := "test/" + uid()
	if err := c.PutContent(ctx, key, "v1"); err != nil {
		t.Fatalf("put v1: %v", err)
	}
	if err := c.PutContent(ctx, key, "v2"); err != nil {
		t.Fatalf("put v2: %v", err)
	}
	got, err := c.GetContent(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != "v2" {
		t.Errorf("got %q want v2", got)
	}
}

func TestTooLarge(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	big := strings.Repeat("x", 1<<20+1)
	err := c.PutContent(ctx, "test/"+uid(), big)
	if !errors.Is(err, store.ErrTooLarge) {
		t.Errorf("expected ErrTooLarge, got %v", err)
	}
}

func TestRoundTripUnicode(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	key := "test/" + uid()
	content := "# 日本語\n\nHello 🌍\n"
	if err := c.PutContent(ctx, key, content); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := c.GetContent(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != content {
		t.Errorf("unicode round-trip failed: got %q want %q", got, content)
	}
}
