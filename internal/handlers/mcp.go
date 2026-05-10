package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/pwntato/notoriousmcp/internal/auth"
	"github.com/pwntato/notoriousmcp/internal/db"
	"github.com/pwntato/notoriousmcp/internal/models"
	"github.com/pwntato/notoriousmcp/internal/store"
)

const (
	mcpProtocolVersion = "2024-11-05"
	serverName         = "notoriousmcp"
	serverVersion      = "0.1.0"
)

const (
	// DefaultStorageCapBytes is 1 GB — overridden by DEFAULT_STORAGE_CAP_BYTES env var.
	DefaultStorageCapBytes int64 = 1 << 30
	// DefaultTransferCapBytes is 1 GB/month — overridden by DEFAULT_TRANSFER_CAP_BYTES env var.
	DefaultTransferCapBytes int64 = 1 << 30
)

// Config holds handler-level configuration (currently just default caps).
type Config struct {
	DefaultStorageCap  int64
	DefaultTransferCap int64
}

// Handler is the MCP protocol handler.
type Handler struct {
	db    *db.Client
	store *store.Client
	cfg   Config
}

// New creates an MCP Handler. Passing nil for dbClient or storeClient is safe
// only for unit tests that exercise code paths not reaching the DB or store.
func New(dbClient *db.Client, storeClient *store.Client, cfg Config) *Handler {
	if cfg.DefaultStorageCap == 0 {
		cfg.DefaultStorageCap = DefaultStorageCapBytes
	}
	if cfg.DefaultTransferCap == 0 {
		cfg.DefaultTransferCap = DefaultTransferCapBytes
	}
	return &Handler{db: dbClient, store: storeClient, cfg: cfg}
}

// effectiveStorageCap returns the storage cap in bytes for a user, falling back
// to the server default. A per-user cap of 0 blocks all writes — intentional.
// Negative values can't be stored (handleUpdateUser rejects them via the >= 0
// guard), but clamp to 0 defensively so the > comparison stays correct.
func (h *Handler) effectiveStorageCap(u *models.User) int64 {
	capBytes := h.cfg.DefaultStorageCap
	if u.StorageCapBytes != nil {
		capBytes = *u.StorageCapBytes
	}
	if capBytes < 0 {
		return 0
	}
	return capBytes
}

// effectiveTransferCap returns the monthly transfer cap in bytes for a user,
// falling back to the server default. Same semantics as effectiveStorageCap.
func (h *Handler) effectiveTransferCap(u *models.User) int64 {
	capBytes := h.cfg.DefaultTransferCap
	if u.TransferCapBytes != nil {
		capBytes = *u.TransferCapBytes
	}
	if capBytes < 0 {
		return 0
	}
	return capBytes
}

// currentMonth returns the current UTC month in YYYY-MM format for transfer records.
func currentMonth() string {
	return time.Now().UTC().Format("2006-01")
}

// transferRecordTTL is how long monthly transfer records are retained. 60 days
// ensures the previous month is always available for debugging while old records
// auto-delete without a cron job.
const transferRecordTTL = 60 * 24 * time.Hour

// transferTTL returns a Unix timestamp transferRecordTTL from now, for use as
// the DynamoDB TTL attribute on TRANSFER#YYYY-MM items.
func transferTTL() int64 {
	return time.Now().UTC().Add(transferRecordTTL).Unix()
}

// readTransferUsed returns the current month's transfer byte total for the user.
// Callers fetch S3 content first, serialize the response, then call this and
// compare the result against effectiveTransferCap to decide whether to block
// (post-fetch by design: response size is only known after serialization).
//
// The read-check-increment is not atomic: two concurrent requests can both pass
// the cap check and both record transfer, exceeding the cap by up to one
// response's worth of bytes. This is accepted as soft enforcement — the cap is
// an abuse guard, not a hard billing limit.
func (h *Handler) readTransferUsed(ctx context.Context, user *models.User) (int64, *rpcError) {
	used, err := h.db.GetTransferUsed(ctx, user.UserID, currentMonth())
	if err != nil {
		log.Printf("mcp: get transfer used for %s: %v", user.UserID, err)
		return 0, &rpcError{Code: codeInternalError, Message: "internal error"}
	}
	return used, nil
}

// RegisterRoutes wires the MCP endpoint onto the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /mcp", h.ServeHTTP)
}

// JSON-RPC 2.0 types.

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// maxRequestBytes caps the MCP request body to slightly above the 1MB content
// limit so a single large tool call is accepted while unbounded payloads are not.
const maxRequestBytes = 2 << 20 // 2MB

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	var req rpcRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, nil, codeParseError, "parse error")
		return
	}
	if req.JSONRPC != "2.0" {
		writeError(w, req.ID, codeInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}

	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, req.ID, codeInternalError, "internal error")
		return
	}

	// MCP notifications have no id field (or id: null). Per spec the server
	// must not send a response for notifications — return 204 silently.
	if isNotification(req.ID) {
		h.dispatch(r, user, req.Method, req.Params) //nolint:errcheck // intentionally ignored
		w.WriteHeader(http.StatusNoContent)
		return
	}

	result, rpcErr := h.dispatch(r, user, req.Method, req.Params)
	if rpcErr != nil {
		writeError(w, req.ID, rpcErr.Code, rpcErr.Message)
		return
	}

	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("mcp: encode response: %v", err)
	}
}

// isNotification returns true when id is absent or null — the JSON-RPC 2.0
// signal that a message is a one-way notification requiring no response.
func isNotification(id json.RawMessage) bool {
	return len(id) == 0 || string(id) == "null"
}

func (h *Handler) dispatch(r *http.Request, user *models.User, method string, params json.RawMessage) (any, *rpcError) {
	switch method {
	case "initialize":
		return h.handleInitialize(params)
	case "tools/list":
		return h.handleToolsList(user)
	case "tools/call":
		return h.handleToolsCall(r, user, params)
	default:
		return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("method not found: %s", method)}
	}
}

// MCP initialize response types.

type initResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

type capabilities struct {
	Tools toolsCap `json:"tools"`
}

type toolsCap struct {
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (h *Handler) handleInitialize(_ json.RawMessage) (any, *rpcError) {
	return initResult{
		ProtocolVersion: mcpProtocolVersion,
		Capabilities:    capabilities{Tools: toolsCap{ListChanged: false}},
		ServerInfo:      serverInfo{Name: serverName, Version: serverVersion},
	}, nil
}

// tools/list

type toolsListResult struct {
	Tools []toolDef `json:"tools"`
}

func (h *Handler) handleToolsList(user *models.User) (any, *rpcError) {
	return toolsListResult{Tools: toolDefsFor(h.toolsForUser(user))}, nil
}

// tools/call

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type toolsCallResult struct {
	Content []toolContent `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

type toolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (h *Handler) handleToolsCall(r *http.Request, user *models.User, params json.RawMessage) (any, *rpcError) {
	var p toolsCallParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "invalid params"}
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}

	allowed := h.toolsForUser(user)
	for _, t := range allowed {
		if t.def.Name == p.Name {
			return t.fn(r.Context(), user, p.Arguments)
		}
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: fmt.Sprintf("unknown tool: %s", p.Name)}
}

// textResult wraps plain text output in an MCP content block.
func textResult(text string) (*toolsCallResult, *rpcError) {
	return &toolsCallResult{Content: []toolContent{{Type: "text", Text: text}}}, nil
}

// errorResult wraps an error message in an MCP content block with IsError=true.
func errorResult(text string) (*toolsCallResult, *rpcError) {
	return &toolsCallResult{
		Content: []toolContent{{Type: "text", Text: text}},
		IsError: true,
	}, nil
}

// jsonResult serialises v as pretty JSON inside an MCP content block.
func jsonResult(v any) (*toolsCallResult, *rpcError) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
	}
	return &toolsCallResult{Content: []toolContent{{Type: "text", Text: string(b)}}}, nil
}

func writeError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// strArg extracts a required string argument.
func strArg(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("missing required argument %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %q must be a string", key)
	}
	return s, nil
}

// strArgOpt extracts an optional string argument.
func strArgOpt(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

// strSliceArgOpt extracts an optional []string argument.
func strSliceArgOpt(args map[string]any, key string) []string {
	v, ok := args[key]
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// parseOptionalTime parses an ISO 8601 timestamp string, returning nil if empty.
func parseOptionalTime(s string) (*time.Time, error) {
	if s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("invalid timestamp %q: must be RFC3339", s)
	}
	return &t, nil
}

// parseModifiedSince validates and normalises a user-supplied modified_since
// string. Returns a normalised RFC3339 string suitable for DB key comparisons,
// or an rpcError if the value is non-empty but unparseable.
func parseModifiedSince(s string) (string, *rpcError) {
	if s == "" {
		return "", nil
	}
	t, err := parseOptionalTime(s)
	if err != nil {
		return "", &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	return t.UTC().Format(time.RFC3339), nil
}

// newID generates a random 16-byte hex ID.
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// int64ArgOpt reads an optional numeric argument as int64, returning (0, false)
// if absent. Returns (value, true) if present; caller should pass -1 to mean
// "clear the cap" so that 0 is distinguishable from "not provided".
// Logs a warning and returns (0, false) if the field is present but not a
// recognized numeric type — treats it the same as absent rather than erroring,
// matching the soft-validation style of other optional args.
func int64ArgOpt(args map[string]any, key string) (int64, bool) {
	v, ok := args[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		log.Printf("mcp: arg %q: unexpected type %T, ignoring", key, v)
		return 0, false
	}
}

// versionArg reads the optional "version" argument as int.
func versionArg(args map[string]any) int {
	v, ok := args["version"]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// dbErrResult translates common db errors to user-facing error results.
func dbErrResult(err error) (*toolsCallResult, *rpcError) {
	if errors.Is(err, db.ErrNotFound) {
		return errorResult("not found")
	}
	if errors.Is(err, db.ErrVersionConflict) {
		return errorResult("version conflict: reload and retry")
	}
	if errors.Is(err, db.ErrStorageCap) {
		return errorResult("Storage cap reached. Contact your administrator to adjust your cap.")
	}
	if errors.Is(err, db.ErrTransferCap) {
		return errorResult("Monthly transfer cap reached. Contact your administrator to adjust your cap.")
	}
	log.Printf("mcp: db error: %v", err)
	return nil, &rpcError{Code: codeInternalError, Message: "internal error"}
}
