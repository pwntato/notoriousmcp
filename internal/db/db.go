package db

import (
	"context"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// Client wraps the DynamoDB client and table name.
type Client struct {
	ddb       *dynamodb.Client
	tableName string
}

// New creates a DynamoDB client. If endpoint is non-empty, it overrides the
// endpoint (for local dev with DynamoDB Local).
func New(ctx context.Context, tableName, endpoint string) (*Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	var ddbOpts []func(*dynamodb.Options)
	if endpoint != "" {
		ddbOpts = append(ddbOpts, func(o *dynamodb.Options) {
			o.BaseEndpoint = &endpoint
		})
	}

	return &Client{
		ddb:       dynamodb.NewFromConfig(cfg, ddbOpts...),
		tableName: tableName,
	}, nil
}

// Item type constants for GSI1PK — using constants prevents typos that would
// silently return empty results.
const (
	itemTypeNote     = "NOTE"
	itemTypeTodoList = "TODOLIST"
	itemTypeTodo     = "TODO"
	itemTypeFile     = "FILE"
)

func pk(userID string) string             { return "USER#" + userID }
func profileSK() string                   { return "PROFILE" }
func noteSK(noteID string) string         { return "NOTE#" + noteID }
func todoListSK(listID string) string     { return "TODOLIST#" + listID }
func todoSK(listID, todoID string) string { return "TODO#" + listID + "#" + todoID }
func fileSK(path string) string           { return "FILE#" + path }

func gsi1PK(userID, itemType string) string { return "USER#" + userID + "#" + itemType }

// gsi1SK produces a sort key that supports > range queries because RFC3339Nano
// sorts lexicographically identically to chronologically — valid only when
// timestamps are always UTC and zero-padded, both enforced by .UTC().Format(isoFormat).
func gsi1SK(modifiedAt, itemID string) string { return "MODIFIED#" + modifiedAt + "#" + itemID }
