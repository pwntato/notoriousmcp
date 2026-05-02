package models

import "time"

type UserStatus string

const (
	StatusPending UserStatus = "pending"
	StatusUser    UserStatus = "user"
	StatusAdmin   UserStatus = "admin"
	StatusBanned  UserStatus = "banned"
)

type User struct {
	UserID       string     `json:"user_id"`
	Email        string     `json:"email"`
	Name         string     `json:"name"`
	Status       UserStatus `json:"status"`
	RefreshToken string     `json:"-"`
	CreatedAt    time.Time  `json:"created_at"`
}

type Note struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Title      string    `json:"title"`
	Tags       []string  `json:"tags"`
	S3Key      string    `json:"s3_key"`
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
	Content    string    `json:"content,omitempty"`
}

type TodoList struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	Title      string    `json:"title"`
	Tags       []string  `json:"tags"`
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
}

type TodoStatus string

const (
	TodoPending    TodoStatus = "pending"
	TodoInProgress TodoStatus = "in_progress"
	TodoDone       TodoStatus = "done"
)

type Todo struct {
	ID         string     `json:"id"`
	ListID     string     `json:"list_id"`
	UserID     string     `json:"user_id"`
	Text       string     `json:"text"`
	Status     TodoStatus `json:"status"`
	DueDate    *time.Time `json:"due_date,omitempty"`
	Tags       []string   `json:"tags"`
	Version    int        `json:"version"`
	CreatedAt  time.Time  `json:"created_at"`
	ModifiedAt time.Time  `json:"modified_at"`
}

type File struct {
	Path       string    `json:"path"`
	UserID     string    `json:"user_id"`
	S3Key      string    `json:"s3_key"`
	Size       int64     `json:"size"`
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"created_at"`
	ModifiedAt time.Time `json:"modified_at"`
	Content    string    `json:"content,omitempty"`
}
