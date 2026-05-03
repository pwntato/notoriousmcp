package db

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
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
	opts := []func(*awsconfig.LoadOptions) error{}
	if endpoint != "" {
		opts = append(opts, awsconfig.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{URL: endpoint, HostnameImmutable: true}, nil
			}),
		))
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	return &Client{
		ddb:       dynamodb.NewFromConfig(cfg),
		tableName: tableName,
	}, nil
}

func pk(userID string) string           { return "USER#" + userID }
func profileSK() string                 { return "PROFILE" }
func noteSK(noteID string) string       { return "NOTE#" + noteID }
func todoListSK(listID string) string   { return "TODOLIST#" + listID }
func todoSK(listID, todoID string) string { return "TODO#" + listID + "#" + todoID }
func fileSK(path string) string         { return "FILE#" + path }

func gsi1PK(userID, itemType string) string { return "USER#" + userID + "#" + itemType }
func gsi1SK(modifiedAt, itemID string) string { return "MODIFIED#" + modifiedAt + "#" + itemID }
