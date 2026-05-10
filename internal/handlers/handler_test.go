package handlers_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/pwntato/notoriousmcp/internal/auth"
	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/handlers"
	"github.com/pwntato/notoriousmcp/internal/models"
	"github.com/pwntato/notoriousmcp/internal/store"
)

// ---- helpers ----

type rpcReq struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// newUnitHandler builds an MCP handler that injects user into context via
// auth.WithUserContext, bypassing auth.Middleware entirely. Safe for tests
// that don't need DB or S3.
func newUnitHandler(user *models.User) http.Handler {
	h := handlers.New(nil, nil, handlers.Config{})
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithUserContext(r.Context(), user)
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
}

func doMCPRequest(t *testing.T, h http.Handler, method string, params any) *rpcResp {
	t.Helper()
	body := rpcReq{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp rpcResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response (body: %s): %v", w.Body.String(), err)
	}
	return &resp
}

// ---- unit tests: protocol layer ----

func TestInitialize(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "initialize", nil)
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}

	var result struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name string `json:"name"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ProtocolVersion == "" {
		t.Error("protocolVersion is empty")
	}
	if result.ServerInfo.Name == "" {
		t.Error("serverInfo.name is empty")
	}
}

func TestInvalidJSONRPCVersion(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	body := `{"jsonrpc":"1.0","id":1,"method":"initialize"}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp rpcResp
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for invalid version")
	}
	if resp.Error.Code != -32600 {
		t.Errorf("code: got %d want -32600 (invalid request)", resp.Error.Code)
	}
}

func TestParseError(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString("{bad json"))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp rpcResp
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error == nil {
		t.Fatal("expected JSON-RPC error for bad JSON")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("code: got %d want -32700 (parse error)", resp.Error.Code)
	}
}

func TestUnknownMethod(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "no/such/method", nil)
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code: got %d want -32601 (method not found)", resp.Error.Code)
	}
}

// ---- unit tests: tool list gating ----

func TestToolsListUserRole(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/list", nil)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}

	tools := parseToolsList(t, resp.Result)
	// 4 notes + 3 todo-lists + 3 todos + 4 files = 14
	if len(tools) != 14 {
		t.Errorf("user role: got %d tools want 14", len(tools))
	}
	for _, name := range []string{"list_users", "update_user"} {
		if toolsContain(tools, name) {
			t.Errorf("user role must not see admin tool %q", name)
		}
	}
	if toolsContain(tools, "check_status") {
		t.Error("user role must not see check_status")
	}
}

func TestToolsListAdminRole(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusAdmin}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/list", nil)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}

	tools := parseToolsList(t, resp.Result)
	// 14 user tools + 2 admin tools = 16
	if len(tools) != 16 {
		t.Errorf("admin role: got %d tools want 16", len(tools))
	}
	for _, name := range []string{"list_users", "update_user"} {
		if !toolsContain(tools, name) {
			t.Errorf("admin role must see tool %q", name)
		}
	}
}

func TestToolsListPendingRole(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusPending}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/list", nil)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}

	tools := parseToolsList(t, resp.Result)
	if len(tools) != 1 {
		t.Errorf("pending role: got %d tools want 1", len(tools))
	}
	if !toolsContain(tools, "check_status") {
		t.Error("pending role must have check_status")
	}
}

func TestToolsListBannedRole(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusBanned}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/list", nil)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}

	tools := parseToolsList(t, resp.Result)
	if len(tools) != 1 || !toolsContain(tools, "check_status") {
		t.Errorf("banned role: got %d tools, want exactly check_status", len(tools))
	}
}

// ---- unit tests: check_status tool ----

func TestCheckStatusPending(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusPending}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/call", map[string]any{
		"name":      "check_status",
		"arguments": map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("check_status error: %+v", resp.Error)
	}
	if text := firstContentText(t, resp.Result); text != "Your account is pending admin approval." {
		t.Errorf("pending: got %q", text)
	}
}

func TestCheckStatusBanned(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusBanned}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/call", map[string]any{
		"name":      "check_status",
		"arguments": map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("check_status error: %+v", resp.Error)
	}
	if text := firstContentText(t, resp.Result); text != "Your account has been banned." {
		t.Errorf("banned: got %q", text)
	}
}

// ---- unit tests: tool access control ----

func TestUserCannotCallAdminTool(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/call", map[string]any{
		"name":      "list_users",
		"arguments": map[string]any{},
	})
	if resp.Error == nil {
		t.Fatal("expected error when user calls admin tool")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code: got %d want -32601", resp.Error.Code)
	}
}

func TestPendingCannotCallUserTool(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusPending}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/call", map[string]any{
		"name":      "search_notes",
		"arguments": map[string]any{},
	})
	if resp.Error == nil {
		t.Fatal("expected error when pending user calls search_notes")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code: got %d want -32601", resp.Error.Code)
	}
}

func TestToolsCallMissingName(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/call", map[string]any{
		"arguments": map[string]any{},
	})
	if resp.Error == nil {
		t.Fatal("expected error for missing tool name")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code: got %d want -32601", resp.Error.Code)
	}
}

func TestSaveTodoInvalidStatus(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/call", map[string]any{
		"name": "save_todo",
		"arguments": map[string]any{
			"list_id": "some-list",
			"text":    "buy milk",
			"status":  "not_a_real_status",
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error for invalid todo status in save_todo")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code: got %d want -32602 (invalid params)", resp.Error.Code)
	}
}

func TestNotificationsInitializedNoOp(t *testing.T) {
	// notifications/initialized has no id (it's a one-way notification per MCP
	// spec). The server must not send a response body — expect 204 No Content.
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	body := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
	req := httptest.NewRequest("POST", "/mcp", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("notifications/initialized: got %d want 204", w.Code)
	}
	if w.Body.Len() != 0 {
		t.Errorf("notifications/initialized: expected empty body, got %q", w.Body.String())
	}
}

func TestListTodosInvalidStatus(t *testing.T) {
	user := &models.User{UserID: "u1", Status: models.StatusUser}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/call", map[string]any{
		"name": "list_todos",
		"arguments": map[string]any{
			"list_id": "some-list",
			"status":  "not_a_real_status",
		},
	})
	if resp.Error == nil {
		t.Fatal("expected error for invalid todo status")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code: got %d want -32602 (invalid params)", resp.Error.Code)
	}
}

func TestCheckStatusNotExposedToActiveUsers(t *testing.T) {
	// check_status is only in the tool list for pending/banned users.
	// Active users (user/admin) cannot call it — the tool list gates access.
	for _, status := range []models.UserStatus{models.StatusUser, models.StatusAdmin} {
		user := &models.User{UserID: "u1", Status: status}
		h := newUnitHandler(user)

		resp := doMCPRequest(t, h, "tools/call", map[string]any{
			"name":      "check_status",
			"arguments": map[string]any{},
		})
		if resp.Error == nil || resp.Error.Code != -32601 {
			t.Errorf("status %s: expected -32601 method-not-found, got error=%v", status, resp.Error)
		}
	}
}

func TestSaveFilePathSanitisation(t *testing.T) {
	// Test only the invalid cases — these are rejected before the db is touched,
	// so a nil db client is fine.
	invalid := []string{"", ".", "../", "../../etc/passwd/../.."}

	for _, input := range invalid {
		// These inputs resolve to empty or "." after path.Clean and are rejected.
		// Inputs like "../../etc/passwd" are NOT rejected — they collapse to
		// "etc/passwd" and are accepted, but remain safely scoped under the
		// user's files/<userID>/ S3 prefix which is the actual namespace boundary.
		user := &models.User{UserID: "u1", Status: models.StatusUser}
		h := newUnitHandler(user)

		resp := doMCPRequest(t, h, "tools/call", map[string]any{
			"name": "save_file",
			"arguments": map[string]any{
				"path":    input,
				"content": "x",
			},
		})
		if resp.Error == nil || resp.Error.Code != -32602 {
			t.Errorf("path %q: expected -32602 invalid params, got error=%v", input, resp.Error)
		}
	}
}

func TestCleanFilePath(t *testing.T) {
	cases := []struct {
		input string
		want  string // empty string means expect an error
	}{
		{"notes/foo.md", "notes/foo.md"},
		{"../../etc/passwd", "etc/passwd"},   // traversal collapses, namespace stays under userID prefix
		{"/absolute/path", "absolute/path"}, // leading slash stripped
		{`back\slash`, "back/slash"},        // backslash normalized
		{"a//b", "a/b"},                     // double slash collapsed
		{"", ""},                            // empty → rejected
		{".", ""},                           // dot → rejected
		{"../", ""},                         // traversal-only → rejected
	}
	for _, tc := range cases {
		got, err := handlers.CleanFilePath(tc.input)
		if tc.want == "" {
			if err == nil {
				t.Errorf("cleanFilePath(%q): expected error, got %q", tc.input, got)
			}
		} else {
			if err != nil {
				t.Errorf("cleanFilePath(%q): unexpected error: %v", tc.input, err)
			} else if got != tc.want {
				t.Errorf("cleanFilePath(%q): got %q want %q", tc.input, got, tc.want)
			}
		}
	}
}

// ---- integration tests (require DYNAMODB_ENDPOINT + S3_ENDPOINT) ----

func newTestClients(t *testing.T) (*db.Client, *store.Client) {
	t.Helper()
	dynamoEndpoint := os.Getenv("DYNAMODB_ENDPOINT")
	if dynamoEndpoint == "" {
		t.Skip("DYNAMODB_ENDPOINT not set; skipping integration test")
	}
	s3Endpoint := os.Getenv("S3_ENDPOINT")
	if s3Endpoint == "" {
		t.Skip("S3_ENDPOINT not set; skipping integration test")
	}
	tableName := os.Getenv("TABLE_NAME")
	if tableName == "" {
		tableName = "notoriousmcp"
	}
	bucket := os.Getenv("S3_BUCKET")
	if bucket == "" {
		bucket = "notoriousmcp"
	}
	dbClient, err := db.New(context.Background(), tableName, dynamoEndpoint)
	if err != nil {
		t.Fatalf("new db client: %v", err)
	}
	storeClient, err := store.New(context.Background(), bucket, s3Endpoint)
	if err != nil {
		t.Fatalf("new store client: %v", err)
	}
	return dbClient, storeClient
}

var testSecret = []byte("test-secret-key-at-least-32-bytes!!")

func saveIntegrationUser(t *testing.T, dbClient *db.Client, status models.UserStatus) *models.User {
	t.Helper()
	userID := newTestUserID()
	u := &models.User{
		UserID:    userID,
		Email:     userID + "@example.com",
		Name:      "Test " + userID,
		Status:    status,
		CreatedAt: time.Now().UTC(),
	}
	if err := dbClient.SaveUser(context.Background(), u); err != nil {
		t.Fatalf("save user: %v", err)
	}
	t.Cleanup(func() { _ = dbClient.DeleteUser(context.Background(), userID) })
	return u
}

// newIntegrationHandler wraps the real handler with auth.Middleware using
// server-default caps (1 GB each). Delegates to newIntegrationHandlerWithConfig.
func newIntegrationHandler(t *testing.T, dbClient *db.Client, storeClient *store.Client) http.Handler {
	t.Helper()
	return newIntegrationHandlerWithConfig(t, dbClient, storeClient, handlers.Config{})
}

func doIntegrationRequest(t *testing.T, h http.Handler, userID string, method string, params any) *rpcResp {
	t.Helper()
	token, err := auth.IssueAccessToken(testSecret, userID)
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	body := rpcReq{JSONRPC: "2.0", ID: 1, Method: method, Params: params}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest("POST", "/mcp", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp rpcResp
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode (body: %s): %v", w.Body.String(), err)
	}
	return &resp
}

func TestIntegrationNoteCreateGetDelete(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	// Create.
	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Integration Test Note",
			"content": "hello world",
			"tags":    []any{"test"},
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note: %+v", resp.Error)
	}
	noteID := extractField(t, resp.Result, "id")

	// Capture the S3 key before updating so we can assert it's cleaned up.
	noteV1, err := dbClient.GetNote(context.Background(), user.UserID, noteID)
	if err != nil {
		t.Fatalf("get note from db: %v", err)
	}
	oldS3Key := noteV1.S3Key

	// Update.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"note_id": noteID,
			"title":   "Integration Test Note",
			"content": "updated content",
			"tags":    []any{"test"},
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note update: %+v", resp.Error)
	}

	// Old S3 key must be gone.
	_, err = storeClient.GetContent(context.Background(), oldS3Key)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old s3 key %q: want ErrNotFound, got %v", oldS3Key, err)
	}

	// Get.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("get_note: %+v", resp.Error)
	}
	if content := extractField(t, resp.Result, "content"); content != "updated content" {
		t.Errorf("content: got %q want \"updated content\"", content)
	}

	// Delete.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "delete_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("delete_note: %+v", resp.Error)
	}
}

func TestIntegrationTodoRoundTrip(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "save_todo_list",
		"arguments": map[string]any{"title": "My List"},
	})
	if resp.Error != nil {
		t.Fatalf("save_todo_list: %+v", resp.Error)
	}
	listID := extractField(t, resp.Result, "id")

	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_todo",
		"arguments": map[string]any{
			"list_id": listID,
			"text":    "buy milk",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_todo: %+v", resp.Error)
	}
	todoID := extractField(t, resp.Result, "id")

	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "list_todos",
		"arguments": map[string]any{"list_id": listID},
	})
	if resp.Error != nil {
		t.Fatalf("list_todos: %+v", resp.Error)
	}

	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "delete_todo",
		"arguments": map[string]any{"list_id": listID, "todo_id": todoID},
	})
	if resp.Error != nil {
		t.Fatalf("delete_todo: %+v", resp.Error)
	}
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "delete_todo_list",
		"arguments": map[string]any{"list_id": listID},
	})
	if resp.Error != nil {
		t.Fatalf("delete_todo_list: %+v", resp.Error)
	}
}

func TestIntegrationFileRoundTrip(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	path := "integration/test-" + newTestUserID() + ".txt"

	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_file",
		"arguments": map[string]any{
			"path":    path,
			"content": "file contents",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_file: %+v", resp.Error)
	}

	// Capture the S3 key before updating so we can assert it's cleaned up.
	fileV1, err := dbClient.GetFile(context.Background(), user.UserID, path)
	if err != nil {
		t.Fatalf("get file from db: %v", err)
	}
	oldS3Key := fileV1.S3Key

	// Update.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_file",
		"arguments": map[string]any{
			"path":    path,
			"content": "updated contents",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_file update: %+v", resp.Error)
	}

	// Old S3 key must be gone.
	_, err = storeClient.GetContent(context.Background(), oldS3Key)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("old s3 key %q: want ErrNotFound, got %v", oldS3Key, err)
	}

	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_file",
		"arguments": map[string]any{"path": path},
	})
	if resp.Error != nil {
		t.Fatalf("get_file: %+v", resp.Error)
	}
	if content := extractField(t, resp.Result, "content"); content != "updated contents" {
		t.Errorf("content: got %q want \"updated contents\"", content)
	}

	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "delete_file",
		"arguments": map[string]any{"path": path},
	})
	if resp.Error != nil {
		t.Fatalf("delete_file: %+v", resp.Error)
	}
}

func TestIntegrationAdminListUsers(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	admin := saveIntegrationUser(t, dbClient, models.StatusAdmin)
	h := newIntegrationHandler(t, dbClient, storeClient)

	resp := doIntegrationRequest(t, h, admin.UserID, "tools/call", map[string]any{
		"name":      "list_users",
		"arguments": map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("list_users: %+v", resp.Error)
	}
}

func TestIntegrationAdminUpdateUser(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	admin := saveIntegrationUser(t, dbClient, models.StatusAdmin)
	target := saveIntegrationUser(t, dbClient, models.StatusPending)
	h := newIntegrationHandler(t, dbClient, storeClient)

	resp := doIntegrationRequest(t, h, admin.UserID, "tools/call", map[string]any{
		"name": "update_user",
		"arguments": map[string]any{
			"user_id": target.UserID,
			"status":  "user",
		},
	})
	if resp.Error != nil {
		t.Fatalf("update_user: %+v", resp.Error)
	}
}

func TestIntegrationVersionConflict(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	// Create a note.
	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Conflict Test",
			"content": "v1",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note v1: %+v", resp.Error)
	}
	noteID := extractField(t, resp.Result, "id")
	t.Cleanup(func() {
		doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
			"name":      "delete_note",
			"arguments": map[string]any{"note_id": noteID},
		})
	})

	// Update once to advance version to 2 — assert it succeeds.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"note_id": noteID,
			"title":   "Conflict Test",
			"content": "v2",
			"version": 2,
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note v2: %+v", resp.Error)
	}
	var v2result struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &v2result); err != nil {
		t.Fatalf("unmarshal v2 result: %v", err)
	}
	if v2result.IsError {
		t.Fatal("save_note v2 should succeed but got isError=true")
	}

	// Attempt to update with stale version 2 again — must get a conflict error.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"note_id": noteID,
			"title":   "Conflict Test",
			"content": "stale",
			"version": 2,
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}
	// The result should be an IsError tool result, not an RPC error.
	var result struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if !result.IsError {
		t.Error("stale-version update: expected isError=true in result")
	}

	// Confirm the stale write was truly rejected: content must still be "v2".
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("get_note after stale write: %+v", resp.Error)
	}
	if content := extractField(t, resp.Result, "content"); content != "v2" {
		t.Errorf("note content after stale write: got %q want \"v2\"", content)
	}
}

func TestIntegrationUserCannotCallAdminTool(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "list_users",
		"arguments": map[string]any{},
	})
	if resp.Error == nil {
		t.Fatal("user role must not be able to call list_users")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code: got %d want -32601", resp.Error.Code)
	}
}

func newIntegrationHandlerWithConfig(t *testing.T, dbClient *db.Client, storeClient *store.Client, cfg handlers.Config) http.Handler {
	t.Helper()
	h := handlers.New(dbClient, storeClient, cfg)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	authCfg := auth.Config{
		ClientID:     "x",
		ClientSecret: "x",
		RedirectURL:  "https://example.com/auth/callback",
		TokenSecret:  testSecret,
	}
	return auth.Middleware(authCfg, dbClient, mux)
}

func TestIntegrationStorageCapEnforced(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	// Cap is 10 bytes — any real content will exceed it.
	h := newIntegrationHandlerWithConfig(t, dbClient, storeClient, handlers.Config{
		DefaultStorageCap:  10,
		DefaultTransferCap: handlers.DefaultTransferCapBytes,
	})

	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Cap Test",
			"content": "this content is definitely more than 10 bytes",
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}
	var result struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true when storage cap exceeded")
	}
	text := firstContentText(t, resp.Result)
	if text == "" {
		t.Error("expected error message in content")
	}
}

func TestIntegrationFileStorageCapEnforced(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	// Cap is 10 bytes — any real file content will exceed it.
	h := newIntegrationHandlerWithConfig(t, dbClient, storeClient, handlers.Config{
		DefaultStorageCap:  10,
		DefaultTransferCap: handlers.DefaultTransferCapBytes,
	})

	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_file",
		"arguments": map[string]any{
			"path":    "test/cap-test.txt",
			"content": "this content is definitely more than 10 bytes",
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}
	var result struct{ IsError bool }
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true when file storage cap exceeded")
	}
}

func TestIntegrationStorageDecrementedOnDelete(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	admin := saveIntegrationUser(t, dbClient, models.StatusAdmin)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	// Save a note to establish some storage usage.
	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Delete Decrement Test",
			"content": "some content",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note: %+v", resp.Error)
	}
	noteID := extractField(t, resp.Result, "id")

	// Capture storage usage after save via list_users.
	usageBefore := getStorageUsedPct(t, h, admin.UserID, user.UserID)

	// Delete the note.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "delete_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("delete_note: %+v", resp.Error)
	}

	// Storage usage should be lower (or zero) after delete.
	usageAfter := getStorageUsedPct(t, h, admin.UserID, user.UserID)
	if usageAfter >= usageBefore {
		t.Errorf("storage_used_pct: want < %d after delete, got %d", usageBefore, usageAfter)
	}
}

// getStorageUsedPct returns the storage_used_pct for targetUserID from list_users.
func getStorageUsedPct(t *testing.T, h http.Handler, adminUserID, targetUserID string) int {
	t.Helper()
	resp := doIntegrationRequest(t, h, adminUserID, "tools/call", map[string]any{
		"name":      "list_users",
		"arguments": map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("list_users: %+v", resp.Error)
	}
	text := firstContentText(t, resp.Result)
	var users []map[string]any
	if err := json.Unmarshal([]byte(text), &users); err != nil {
		t.Fatalf("unmarshal users: %v", err)
	}
	for _, u := range users {
		if u["user_id"] == targetUserID {
			if pct, ok := u["storage_used_pct"].(float64); ok {
				return int(pct)
			}
		}
	}
	t.Fatalf("user %s not found in list_users", targetUserID)
	return 0
}

func TestIntegrationTransferCapEnforced(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	// Transfer cap is 1 byte — any real get response will exceed it.
	h := newIntegrationHandlerWithConfig(t, dbClient, storeClient, handlers.Config{
		DefaultStorageCap:  handlers.DefaultStorageCapBytes,
		DefaultTransferCap: 1,
	})

	// Create a note (doesn't hit transfer cap).
	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Transfer Cap Test",
			"content": "hello",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note: %+v", resp.Error)
	}
	noteID := extractField(t, resp.Result, "id")
	t.Cleanup(func() {
		doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
			"name":      "delete_note",
			"arguments": map[string]any{"note_id": noteID},
		})
	})

	// get_note must be blocked by the transfer cap.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}
	var result struct {
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true when transfer cap exceeded")
	}
}

func TestIntegrationUsageVisibleInListUsers(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	admin := saveIntegrationUser(t, dbClient, models.StatusAdmin)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	// Save a note to accumulate storage usage.
	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Usage Test",
			"content": "some content for storage tracking",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note: %+v", resp.Error)
	}
	noteID := extractField(t, resp.Result, "id")
	t.Cleanup(func() {
		doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
			"name":      "delete_note",
			"arguments": map[string]any{"note_id": noteID},
		})
	})

	// Fetch the note to accumulate transfer usage.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("get_note: %+v", resp.Error)
	}

	// Admin lists users — find our user and verify both usage percentages are > 0.
	resp = doIntegrationRequest(t, h, admin.UserID, "tools/call", map[string]any{
		"name":      "list_users",
		"arguments": map[string]any{},
	})
	if resp.Error != nil {
		t.Fatalf("list_users: %+v", resp.Error)
	}

	text := firstContentText(t, resp.Result)
	var users []map[string]any
	if err := json.Unmarshal([]byte(text), &users); err != nil {
		t.Fatalf("unmarshal users: %v", err)
	}

	found := false
	for _, u := range users {
		if u["user_id"] == user.UserID {
			found = true
			if _, ok := u["storage_used_pct"]; !ok {
				t.Error("storage_used_pct missing from list_users response")
			}
			if _, ok := u["transfer_used_pct"]; !ok {
				t.Error("transfer_used_pct missing from list_users response")
			}
			if pct, ok := u["storage_used_pct"].(float64); !ok || pct <= 0 {
				t.Errorf("storage_used_pct: want > 0 after save, got %v", u["storage_used_pct"])
			}
			if pct, ok := u["transfer_used_pct"].(float64); !ok || pct <= 0 {
				t.Errorf("transfer_used_pct: want > 0 after get_note, got %v", u["transfer_used_pct"])
			}
		}
	}
	if !found {
		t.Errorf("user %s not found in list_users response", user.UserID)
	}
}

func TestIntegrationPerUserCapOverride(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	admin := saveIntegrationUser(t, dbClient, models.StatusAdmin)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	// Set a tiny storage cap (10 bytes) via update_user — no status change.
	resp := doIntegrationRequest(t, h, admin.UserID, "tools/call", map[string]any{
		"name": "update_user",
		"arguments": map[string]any{
			"user_id":           user.UserID,
			"storage_cap_bytes": float64(10),
		},
	})
	if resp.Error != nil {
		t.Fatalf("update_user (set cap): %+v", resp.Error)
	}
	var updateResult struct{ IsError bool }
	if err := json.Unmarshal(resp.Result, &updateResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if updateResult.IsError {
		t.Fatalf("update_user (set cap): unexpected isError=true: %s", firstContentText(t, resp.Result))
	}

	// save_note must be blocked by the tiny per-user cap.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Cap Override Test",
			"content": "this is more than 10 bytes of content",
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}
	var saveResult struct{ IsError bool }
	if err := json.Unmarshal(resp.Result, &saveResult); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !saveResult.IsError {
		t.Error("expected isError=true when per-user storage cap exceeded")
	}

	// Clear the cap via -1; save should now succeed.
	resp = doIntegrationRequest(t, h, admin.UserID, "tools/call", map[string]any{
		"name": "update_user",
		"arguments": map[string]any{
			"user_id":           user.UserID,
			"storage_cap_bytes": float64(-1),
		},
	})
	if resp.Error != nil {
		t.Fatalf("update_user (clear cap): %+v", resp.Error)
	}

	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Cap Override Test",
			"content": "this is more than 10 bytes of content",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note after cap cleared: %+v", resp.Error)
	}
	noteID := extractField(t, resp.Result, "id")
	t.Cleanup(func() {
		doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
			"name":      "delete_note",
			"arguments": map[string]any{"note_id": noteID},
		})
	})
}

func TestIntegrationStorageCapEnforcedOnUpdate(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	// Cap is 50 bytes — enough for the initial save but not the larger update.
	h := newIntegrationHandlerWithConfig(t, dbClient, storeClient, handlers.Config{
		DefaultStorageCap:  50,
		DefaultTransferCap: handlers.DefaultTransferCapBytes,
	})

	// Create a small note that fits under the cap.
	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Cap Update Test",
			"content": "small",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note (create): %+v", resp.Error)
	}
	var createResult struct{ IsError bool }
	if err := json.Unmarshal(resp.Result, &createResult); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if createResult.IsError {
		t.Fatalf("save_note (create) unexpectedly blocked: %s", firstContentText(t, resp.Result))
	}
	noteID := extractField(t, resp.Result, "id")
	t.Cleanup(func() {
		doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
			"name":      "delete_note",
			"arguments": map[string]any{"note_id": noteID},
		})
	})

	// Update with content large enough to push the total over the 50-byte cap.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"note_id": noteID,
			"title":   "Cap Update Test",
			"content": "this updated content is definitely more than fifty bytes total",
		},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error on update: %+v", resp.Error)
	}
	var updateResult struct{ IsError bool }
	if err := json.Unmarshal(resp.Result, &updateResult); err != nil {
		t.Fatalf("unmarshal update: %v", err)
	}
	if !updateResult.IsError {
		t.Error("expected isError=true when update pushes note past storage cap")
	}
}

func TestIntegrationPerUserTransferCapOverride(t *testing.T) {
	dbClient, storeClient := newTestClients(t)
	admin := saveIntegrationUser(t, dbClient, models.StatusAdmin)
	user := saveIntegrationUser(t, dbClient, models.StatusUser)
	h := newIntegrationHandler(t, dbClient, storeClient)

	// Create a note first (no cap concerns on write).
	resp := doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name": "save_note",
		"arguments": map[string]any{
			"title":   "Transfer Cap Override Test",
			"content": "this is more than one byte of content",
		},
	})
	if resp.Error != nil {
		t.Fatalf("save_note: %+v", resp.Error)
	}
	noteID := extractField(t, resp.Result, "id")
	t.Cleanup(func() {
		doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
			"name":      "delete_note",
			"arguments": map[string]any{"note_id": noteID},
		})
	})

	// Set a 1-byte transfer cap via update_user (no status change).
	resp = doIntegrationRequest(t, h, admin.UserID, "tools/call", map[string]any{
		"name": "update_user",
		"arguments": map[string]any{
			"user_id":            user.UserID,
			"transfer_cap_bytes": float64(1),
		},
	})
	if resp.Error != nil {
		t.Fatalf("update_user (set transfer cap): %+v", resp.Error)
	}

	// get_note must be blocked by the tiny per-user transfer cap.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error: %+v", resp.Error)
	}
	var result struct{ IsError bool }
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true when per-user transfer cap exceeded")
	}

	// Clear the cap; get_note should now succeed.
	resp = doIntegrationRequest(t, h, admin.UserID, "tools/call", map[string]any{
		"name": "update_user",
		"arguments": map[string]any{
			"user_id":            user.UserID,
			"transfer_cap_bytes": float64(-1),
		},
	})
	if resp.Error != nil {
		t.Fatalf("update_user (clear transfer cap): %+v", resp.Error)
	}

	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("get_note after cap cleared: %+v", resp.Error)
	}
	var result2 struct{ IsError bool }
	if err := json.Unmarshal(resp.Result, &result2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result2.IsError {
		t.Errorf("expected success after cap cleared, got: %s", firstContentText(t, resp.Result))
	}
}

func TestUpdateUserNoFields(t *testing.T) {
	// update_user with only user_id and no other fields should return -32602.
	user := &models.User{UserID: "u1", Status: models.StatusAdmin}
	h := newUnitHandler(user)

	resp := doMCPRequest(t, h, "tools/call", map[string]any{
		"name":      "update_user",
		"arguments": map[string]any{"user_id": "u2"},
	})
	if resp.Error == nil {
		t.Fatal("expected error when no fields provided to update_user")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("code: got %d want -32602 (invalid params)", resp.Error.Code)
	}
}

// ---- test helpers ----

type toolEntry struct {
	Name string `json:"name"`
}

func parseToolsList(t *testing.T, raw json.RawMessage) []toolEntry {
	t.Helper()
	var result struct {
		Tools []toolEntry `json:"tools"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse tools list: %v (raw: %s)", err, raw)
	}
	return result.Tools
}

func toolsContain(tools []toolEntry, name string) bool {
	for _, tool := range tools {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func firstContentText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse content: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("no content items in result")
	}
	return result.Content[0].Text
}

// extractField parses the toolsCallResult Content[0].Text as JSON and returns the
// value of the given top-level string field.
func extractField(t *testing.T, raw json.RawMessage, field string) string {
	t.Helper()
	text := firstContentText(t, raw)
	var m map[string]any
	if err := json.Unmarshal([]byte(text), &m); err != nil {
		t.Fatalf("parse content as JSON: %v (text: %s)", err, text)
	}
	v, _ := m[field].(string)
	return v
}

func newTestUserID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
