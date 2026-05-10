package models

import "time"

type UserStatus string

const (
	StatusPending UserStatus = "pending"
	StatusUser    UserStatus = "user"
	StatusAdmin   UserStatus = "admin"
	StatusBanned  UserStatus = "banned"
)

// User is the user profile model. The storage/cap fields are included in JSON
// because User is only serialized in the admin-only list_users response (via
// userWithUsage). No public endpoint serializes User directly — check_status
// returns plain text, and auth middleware only injects User into context.
// If User is ever serialized in a non-admin context, revisit which fields to expose.
type User struct {
	UserID           string     `json:"user_id"             dynamodbav:"UserID"`
	Email            string     `json:"email"               dynamodbav:"Email"`
	Name             string     `json:"name"                dynamodbav:"Name"`
	Status           UserStatus `json:"status"              dynamodbav:"Status"`
	RefreshToken     string     `json:"-"                   dynamodbav:"-"`
	CreatedAt        time.Time  `json:"created_at"          dynamodbav:"CreatedAt"`
	StorageUsedBytes int64      `json:"storage_used_bytes"  dynamodbav:"StorageUsedBytes"`
	StorageCapBytes  *int64     `json:"storage_cap_bytes"   dynamodbav:"StorageCapBytes,omitempty"`
	TransferCapBytes *int64     `json:"transfer_cap_bytes"  dynamodbav:"TransferCapBytes,omitempty"`
}

type Note struct {
	ID         string    `json:"id"          dynamodbav:"ID"`
	UserID     string    `json:"user_id"     dynamodbav:"UserID"`
	Title      string    `json:"title"       dynamodbav:"Title"`
	Tags       []string  `json:"tags"        dynamodbav:"Tags"`
	S3Key      string    `json:"-"           dynamodbav:"S3Key"`
	Size       int64     `json:"size"        dynamodbav:"Size"`
	Version    int       `json:"version"     dynamodbav:"Version"`
	CreatedAt  time.Time `json:"created_at"  dynamodbav:"CreatedAt"`
	ModifiedAt time.Time `json:"modified_at" dynamodbav:"ModifiedAt"`
	Content    string    `json:"content,omitempty" dynamodbav:"-"`
}

type TodoList struct {
	ID         string    `json:"id"          dynamodbav:"ID"`
	UserID     string    `json:"user_id"     dynamodbav:"UserID"`
	Title      string    `json:"title"       dynamodbav:"Title"`
	Tags       []string  `json:"tags"        dynamodbav:"Tags"`
	Version    int       `json:"version"     dynamodbav:"Version"`
	CreatedAt  time.Time `json:"created_at"  dynamodbav:"CreatedAt"`
	ModifiedAt time.Time `json:"modified_at" dynamodbav:"ModifiedAt"`
}

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoDone       TodoStatus = "done"
)

type Todo struct {
	ID         string     `json:"id"          dynamodbav:"ID"`
	ListID     string     `json:"list_id"     dynamodbav:"ListID"`
	UserID     string     `json:"user_id"     dynamodbav:"UserID"`
	Text       string     `json:"text"        dynamodbav:"Text"`
	Status     TodoStatus `json:"status"      dynamodbav:"Status"`
	DueDate    *time.Time `json:"due_date,omitempty" dynamodbav:"DueDate,omitempty"`
	Tags       []string   `json:"tags"        dynamodbav:"Tags"`
	Version    int        `json:"version"     dynamodbav:"Version"`
	CreatedAt  time.Time  `json:"created_at"  dynamodbav:"CreatedAt"`
	ModifiedAt time.Time  `json:"modified_at" dynamodbav:"ModifiedAt"`
}

type File struct {
	Path       string    `json:"path"        dynamodbav:"Path"`
	UserID     string    `json:"user_id"     dynamodbav:"UserID"`
	S3Key      string    `json:"-"           dynamodbav:"S3Key"`
	Size       int64     `json:"size"        dynamodbav:"Size"`
	Version    int       `json:"version"     dynamodbav:"Version"`
	CreatedAt  time.Time `json:"created_at"  dynamodbav:"CreatedAt"`
	ModifiedAt time.Time `json:"modified_at" dynamodbav:"ModifiedAt"`
	Content    string    `json:"content,omitempty" dynamodbav:"-"`
}
