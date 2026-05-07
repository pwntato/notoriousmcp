package handlers_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
	h := handlers.New(nil, nil)
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
		// Feed a path that after path.Clean + trim resolves to empty or ".".
		// Only truly empty / dot results are rejected; traversal sequences that
		// still produce a non-empty path (e.g. "../../etc/passwd") are cleaned
		// and accepted (the result is "etc/passwd").
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
	return u
}

// newIntegrationHandler wraps the real handler with auth.Middleware so that
// real Bearer tokens are validated against DynamoDB.
func newIntegrationHandler(t *testing.T, dbClient *db.Client, storeClient *store.Client) http.Handler {
	t.Helper()
	h := handlers.New(dbClient, storeClient)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	cfg := auth.Config{
		ClientID:     "x",
		ClientSecret: "x",
		RedirectURL:  "https://example.com/auth/callback",
		TokenSecret:  testSecret,
	}
	return auth.Middleware(cfg, dbClient, mux)
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

	// Get.
	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_note",
		"arguments": map[string]any{"note_id": noteID},
	})
	if resp.Error != nil {
		t.Fatalf("get_note: %+v", resp.Error)
	}
	if content := extractField(t, resp.Result, "content"); content != "hello world" {
		t.Errorf("content: got %q want \"hello world\"", content)
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

	resp = doIntegrationRequest(t, h, user.UserID, "tools/call", map[string]any{
		"name":      "get_file",
		"arguments": map[string]any{"path": path},
	})
	if resp.Error != nil {
		t.Fatalf("get_file: %+v", resp.Error)
	}
	if content := extractField(t, resp.Result, "content"); content != "file contents" {
		t.Errorf("content: got %q", content)
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
