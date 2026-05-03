package db_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
)

// uid returns a unique random string to isolate test data across runs.
func uid() string { return fmt.Sprintf("%x", rand.Uint64()) }

func newTestClient(t *testing.T) *db.Client {
	t.Helper()
	endpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if endpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set; skipping integration test")
	}
	tableName := os.Getenv("TABLE_NAME")
	if tableName == "" {
		tableName = "notoriousmcp"
	}
	c, err := db.New(context.Background(), tableName, endpoint)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func TestUserRoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	u := &models.User{
		UserID:    uid(),
		Email:     "test@example.com",
		Name:      "Test User",
		Status:    models.StatusPending,
		CreatedAt: time.Now().UTC().Truncate(time.Second),
	}

	if err := c.SaveUser(ctx, u); err != nil {
		t.Fatalf("save user: %v", err)
	}

	got, err := c.GetUser(ctx, u.UserID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Email != u.Email {
		t.Errorf("email: got %q want %q", got.Email, u.Email)
	}
	if got.Status != models.StatusPending {
		t.Errorf("status: got %q want pending", got.Status)
	}
}

func TestRefreshToken(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	u := &models.User{
		UserID:    uid(),
		Email:     "rt@example.com",
		Name:      "RT User",
		Status:    models.StatusUser,
		CreatedAt: time.Now().UTC(),
	}
	if err := c.SaveUser(ctx, u); err != nil {
		t.Fatalf("save user: %v", err)
	}
	if err := c.SaveRefreshToken(ctx, u.UserID, "token-abc"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}
	token, err := c.LoadRefreshToken(ctx, u.UserID)
	if err != nil {
		t.Fatalf("load refresh token: %v", err)
	}
	if token != "token-abc" {
		t.Errorf("token: got %q want %q", token, "token-abc")
	}
}

func TestUpdateUserStatus(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	u := &models.User{
		UserID:    uid(),
		Email:     "status@example.com",
		Name:      "Status User",
		Status:    models.StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := c.SaveUser(ctx, u); err != nil {
		t.Fatalf("save user: %v", err)
	}
	if err := c.UpdateUserStatus(ctx, u.UserID, models.StatusUser); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, err := c.GetUser(ctx, u.UserID)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if got.Status != models.StatusUser {
		t.Errorf("status: got %q want user", got.Status)
	}
}

func TestGetUserNotFound(t *testing.T) {
	c := newTestClient(t)
	_, err := c.GetUser(context.Background(), "nonexistent-"+uid())
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestSaveUserIdempotency(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Second)
	u := &models.User{
		UserID:    uid(),
		Email:     "idem@example.com",
		Name:      "Idem User",
		Status:    models.StatusPending,
		CreatedAt: now,
	}
	if err := c.SaveUser(ctx, u); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Second save with a different CreatedAt should not overwrite the original;
	// Name update should take effect.
	u.CreatedAt = now.Add(time.Hour)
	u.Name = "Updated Name"
	if err := c.SaveUser(ctx, u); err != nil {
		t.Fatalf("second save: %v", err)
	}

	got, err := c.GetUser(ctx, u.UserID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt was overwritten: got %v want %v", got.CreatedAt, now)
	}
	if got.Name != "Updated Name" {
		t.Errorf("Name not updated: got %q", got.Name)
	}
}

func TestNoteRoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID := uid()
	noteID := uid()
	now := time.Now().UTC().Truncate(time.Second)
	n := &models.Note{
		ID:         noteID,
		UserID:     userID,
		Title:      "Test Note",
		Tags:       []string{"test", "go"},
		S3Key:      "users/" + userID + "/notes/" + noteID,
		Version:    1,
		CreatedAt:  now,
		ModifiedAt: now,
	}

	if err := c.SaveNote(ctx, n); err != nil {
		t.Fatalf("save note: %v", err)
	}
	got, err := c.GetNote(ctx, n.UserID, n.ID)
	if err != nil {
		t.Fatalf("get note: %v", err)
	}
	if got.Title != n.Title {
		t.Errorf("title: got %q want %q", got.Title, n.Title)
	}
	if len(got.Tags) != 2 {
		t.Errorf("tags: got %v want 2 tags", got.Tags)
	}
	if got.Version != 1 {
		t.Errorf("version: got %d want 1", got.Version)
	}
}

func TestNoteVersionConflict(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID, noteID := uid(), uid()
	now := time.Now().UTC()
	n := &models.Note{
		ID: noteID, UserID: userID,
		Title: "v1", S3Key: "x", Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveNote(ctx, n); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	n2 := *n
	n2.Title = "v2"
	n2.Version = 2
	if err := c.SaveNote(ctx, &n2); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	// Stale write: claiming prev=1 but current version is 2
	n3 := *n
	n3.Title = "stale"
	n3.Version = 2
	if err := c.SaveNote(ctx, &n3); !errors.Is(err, db.ErrVersionConflict) {
		t.Errorf("expected version conflict, got %v", err)
	}
}

func TestListNotes(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID := uid()
	now := time.Now().UTC()
	for i := range 3 {
		n := &models.Note{
			ID: uid(), UserID: userID,
			Title: fmt.Sprintf("Note %d", i), S3Key: "x", Version: 1,
			CreatedAt: now, ModifiedAt: now.Add(time.Duration(i) * time.Second),
		}
		if err := c.SaveNote(ctx, n); err != nil {
			t.Fatalf("save note %d: %v", i, err)
		}
	}
	notes, err := c.ListNotes(ctx, userID, "")
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 3 {
		t.Errorf("got %d notes, want 3", len(notes))
	}
}

func TestListNotesModifiedSince(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID := uid()
	base := time.Now().UTC().Truncate(time.Second)
	old := &models.Note{
		ID: uid(), UserID: userID,
		Title: "Old", S3Key: "x", Version: 1,
		CreatedAt: base.Add(-2 * time.Minute), ModifiedAt: base.Add(-2 * time.Minute),
	}
	recent := &models.Note{
		ID: uid(), UserID: userID,
		Title: "Recent", S3Key: "x", Version: 1,
		CreatedAt: base, ModifiedAt: base,
	}
	for _, n := range []*models.Note{old, recent} {
		if err := c.SaveNote(ctx, n); err != nil {
			t.Fatalf("save note: %v", err)
		}
	}

	since := base.Add(-time.Minute).UTC().Format(time.RFC3339)
	notes, err := c.ListNotes(ctx, userID, since)
	if err != nil {
		t.Fatalf("list notes: %v", err)
	}
	if len(notes) != 1 {
		t.Errorf("got %d notes, want 1", len(notes))
	}
	if len(notes) > 0 && notes[0].Title != "Recent" {
		t.Errorf("got %q, want Recent", notes[0].Title)
	}
}

func TestDeleteNote(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID, noteID := uid(), uid()
	now := time.Now().UTC()
	n := &models.Note{
		ID: noteID, UserID: userID,
		Title: "Delete Me", S3Key: "x", Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveNote(ctx, n); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := c.DeleteNote(ctx, n.UserID, n.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := c.GetNote(ctx, n.UserID, n.ID)
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestTodoListRoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	now := time.Now().UTC()
	l := &models.TodoList{
		ID: uid(), UserID: uid(),
		Title: "My List", Tags: []string{"work"},
		Version: 1, CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveTodoList(ctx, l); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := c.GetTodoList(ctx, l.UserID, l.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != l.Title {
		t.Errorf("title: got %q want %q", got.Title, l.Title)
	}
}

func TestTodoListVersionConflict(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	now := time.Now().UTC()
	l := &models.TodoList{
		ID: uid(), UserID: uid(),
		Title: "v1", Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveTodoList(ctx, l); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	l2 := *l
	l2.Version = 2
	if err := c.SaveTodoList(ctx, &l2); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	l3 := *l
	l3.Version = 2
	if err := c.SaveTodoList(ctx, &l3); !errors.Is(err, db.ErrVersionConflict) {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}
}

func TestDeleteTodoList(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	now := time.Now().UTC()
	l := &models.TodoList{
		ID: uid(), UserID: uid(),
		Title: "delete me", Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveTodoList(ctx, l); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := c.DeleteTodoList(ctx, l.UserID, l.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := c.GetTodoList(ctx, l.UserID, l.ID)
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestListTodoLists(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID := uid()
	now := time.Now().UTC()
	for i := range 3 {
		l := &models.TodoList{
			ID: uid(), UserID: userID,
			Title: fmt.Sprintf("List %d", i), Version: 1,
			CreatedAt: now, ModifiedAt: now,
		}
		if err := c.SaveTodoList(ctx, l); err != nil {
			t.Fatalf("save list %d: %v", i, err)
		}
	}
	lists, err := c.ListTodoLists(ctx, userID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(lists) != 3 {
		t.Errorf("got %d lists, want 3", len(lists))
	}
}

func TestTodoRoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	now := time.Now().UTC()
	todo := &models.Todo{
		ID: uid(), ListID: uid(), UserID: uid(),
		Text: "Do the thing", Status: models.TodoPending,
		Tags: []string{"urgent"}, Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveTodo(ctx, todo); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := c.GetTodo(ctx, todo.UserID, todo.ListID, todo.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Text != todo.Text {
		t.Errorf("text: got %q want %q", got.Text, todo.Text)
	}
	if got.Status != models.TodoPending {
		t.Errorf("status: got %q want pending", got.Status)
	}
}

func TestTodoVersionConflict(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID, listID, todoID := uid(), uid(), uid()
	now := time.Now().UTC()
	todo := &models.Todo{
		ID: todoID, ListID: listID, UserID: userID,
		Text: "v1", Status: models.TodoPending, Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveTodo(ctx, todo); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	t2 := *todo
	t2.Version = 2
	if err := c.SaveTodo(ctx, &t2); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	t3 := *todo
	t3.Version = 2
	if err := c.SaveTodo(ctx, &t3); !errors.Is(err, db.ErrVersionConflict) {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}
}

func TestDeleteTodo(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID, listID, todoID := uid(), uid(), uid()
	now := time.Now().UTC()
	todo := &models.Todo{
		ID: todoID, ListID: listID, UserID: userID,
		Text: "delete me", Status: models.TodoPending, Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveTodo(ctx, todo); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := c.DeleteTodo(ctx, todo.UserID, todo.ListID, todo.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := c.GetTodo(ctx, todo.UserID, todo.ListID, todo.ID)
	if !errors.Is(err, db.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestFileRoundTrip(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID := uid()
	now := time.Now().UTC()
	f := &models.File{
		Path: "memory/MEMORY.md", UserID: userID,
		S3Key: "users/" + userID + "/files/memory/MEMORY.md",
		Size:  1024, Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveFile(ctx, f); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := c.GetFile(ctx, f.UserID, f.Path)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Path != f.Path {
		t.Errorf("path: got %q want %q", got.Path, f.Path)
	}
	if got.Size != f.Size {
		t.Errorf("size: got %d want %d", got.Size, f.Size)
	}
}

func TestFileVersionConflict(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID := uid()
	now := time.Now().UTC()
	f := &models.File{
		Path: uid() + ".md", UserID: userID,
		S3Key: "x", Size: 1, Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveFile(ctx, f); err != nil {
		t.Fatalf("save v1: %v", err)
	}
	f2 := *f
	f2.Version = 2
	if err := c.SaveFile(ctx, &f2); err != nil {
		t.Fatalf("save v2: %v", err)
	}
	f3 := *f
	f3.Version = 2
	if err := c.SaveFile(ctx, &f3); !errors.Is(err, db.ErrVersionConflict) {
		t.Errorf("expected ErrVersionConflict, got %v", err)
	}
}

func TestListFiles(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID := uid()
	now := time.Now().UTC()
	for i := range 2 {
		f := &models.File{
			Path: fmt.Sprintf("%s-file-%d.md", uid(), i), UserID: userID,
			S3Key: uid(), Size: 100, Version: 1,
			CreatedAt: now, ModifiedAt: now,
		}
		if err := c.SaveFile(ctx, f); err != nil {
			t.Fatalf("save file %d: %v", i, err)
		}
	}
	files, err := c.ListFiles(ctx, userID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("got %d files, want 2", len(files))
	}
}

func TestListTodosModifiedSinceScoped(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID := uid()
	listA, listB := uid(), uid()
	now := time.Now().UTC()

	for i := range 2 {
		todo := &models.Todo{
			ID: uid(), ListID: listA, UserID: userID,
			Text: "a", Status: models.TodoPending, Version: 1,
			CreatedAt: now, ModifiedAt: now,
		}
		if err := c.SaveTodo(ctx, todo); err != nil {
			t.Fatalf("save list-a todo %d: %v", i, err)
		}
	}
	todoB := &models.Todo{
		ID: uid(), ListID: listB, UserID: userID,
		Text: "b", Status: models.TodoPending, Version: 1,
		CreatedAt: now, ModifiedAt: now,
	}
	if err := c.SaveTodo(ctx, todoB); err != nil {
		t.Fatalf("save list-b todo: %v", err)
	}

	since := now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	todos, err := c.ListTodos(ctx, userID, listA, since, nil)
	if err != nil {
		t.Fatalf("list todos: %v", err)
	}
	for _, td := range todos {
		if td.ListID != listA {
			t.Errorf("got todo from list %q, want only %q", td.ListID, listA)
		}
	}
	if len(todos) != 2 {
		t.Errorf("got %d todos, want 2", len(todos))
	}
}

func TestListTodosModifiedSinceAndStatus(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID, listID := uid(), uid()
	now := time.Now().UTC()

	statuses := []models.TodoStatus{models.TodoPending, models.TodoDone, models.TodoPending}
	for i, s := range statuses {
		todo := &models.Todo{
			ID: uid(), ListID: listID, UserID: userID,
			Text: fmt.Sprintf("todo %d", i), Status: s, Version: 1,
			CreatedAt: now, ModifiedAt: now,
		}
		if err := c.SaveTodo(ctx, todo); err != nil {
			t.Fatalf("save todo: %v", err)
		}
	}

	since := now.Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	pending := models.TodoPending
	todos, err := c.ListTodos(ctx, userID, listID, since, &pending)
	if err != nil {
		t.Fatalf("list todos: %v", err)
	}
	if len(todos) != 2 {
		t.Errorf("got %d todos, want 2 pending", len(todos))
	}
	for _, td := range todos {
		if td.Status != models.TodoPending {
			t.Errorf("got status %q, want pending", td.Status)
		}
		if td.ListID != listID {
			t.Errorf("got list %q, want %q", td.ListID, listID)
		}
	}
}

func TestListTodosNoModifiedSince(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	userID, listID := uid(), uid()
	now := time.Now().UTC()
	for i := range 3 {
		todo := &models.Todo{
			ID: uid(), ListID: listID, UserID: userID,
			Text: fmt.Sprintf("todo %d", i), Status: models.TodoPending, Version: 1,
			CreatedAt: now, ModifiedAt: now,
		}
		if err := c.SaveTodo(ctx, todo); err != nil {
			t.Fatalf("save todo %d: %v", i, err)
		}
	}
	todos, err := c.ListTodos(ctx, userID, listID, "", nil)
	if err != nil {
		t.Fatalf("list todos: %v", err)
	}
	if len(todos) != 3 {
		t.Errorf("got %d todos, want 3", len(todos))
	}
}

func TestLoadRefreshTokenNoAttribute(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	// Create user without ever calling SaveRefreshToken
	u := &models.User{
		UserID:    uid(),
		Email:     "notoken@example.com",
		Name:      "No Token",
		Status:    models.StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := c.SaveUser(ctx, u); err != nil {
		t.Fatalf("save user: %v", err)
	}
	_, err := c.LoadRefreshToken(ctx, u.UserID)
	if !errors.Is(err, db.ErrNoRefreshToken) {
		t.Errorf("expected ErrNoRefreshToken, got %v", err)
	}
}

func TestSaveUserPreservesRefreshToken(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()

	u := &models.User{
		UserID:    uid(),
		Email:     "preserve@example.com",
		Name:      "Preserve Token",
		Status:    models.StatusPending,
		CreatedAt: time.Now().UTC(),
	}
	if err := c.SaveUser(ctx, u); err != nil {
		t.Fatalf("save user: %v", err)
	}
	if err := c.SaveRefreshToken(ctx, u.UserID, "my-token"); err != nil {
		t.Fatalf("save refresh token: %v", err)
	}
	// Re-save user profile — must not overwrite token
	u.Status = models.StatusUser
	if err := c.SaveUser(ctx, u); err != nil {
		t.Fatalf("re-save user: %v", err)
	}
	token, err := c.LoadRefreshToken(ctx, u.UserID)
	if err != nil {
		t.Fatalf("load refresh token: %v", err)
	}
	if token != "my-token" {
		t.Errorf("token was overwritten: got %q want %q", token, "my-token")
	}
}
