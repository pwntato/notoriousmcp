package db

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// clientRecordSK is the sort key for registered client items. Named to avoid
// confusion with clientPK which uses the "CLIENT#" prefix on the partition key.
const clientRecordSK = "CLIENT"

type clientRecord struct {
	PK          string `dynamodbav:"PK"`
	SK          string `dynamodbav:"SK"`
	ClientID    string `dynamodbav:"ClientID"`
	ClientName  string `dynamodbav:"ClientName,omitempty"`
	RedirectURI string `dynamodbav:"RedirectURI"`
	IssuedAt    int64  `dynamodbav:"IssuedAt"`
	ExpiresAt   int64  `dynamodbav:"ExpiresAt"`
}

func clientPK(clientID string) string { return "CLIENT#" + clientID }

const clientTTL = 90 * 24 * time.Hour

// RegisteredClient holds the values for a dynamically registered OAuth client.
type RegisteredClient struct {
	ClientID    string
	RedirectURI string
	IssuedAt    int64
}

// SaveClient persists a dynamically registered OAuth client (RFC 7591).
func (c *Client) SaveClient(ctx context.Context, clientID, redirectURI, clientName string) error {
	now := time.Now()
	rec := clientRecord{
		PK:          clientPK(clientID),
		SK:          clientRecordSK,
		ClientID:    clientID,
		ClientName:  clientName,
		RedirectURI: redirectURI,
		IssuedAt:    now.Unix(),
		ExpiresAt:   now.Add(clientTTL).Unix(),
	}
	item, err := attributevalue.MarshalMap(rec)
	if err != nil {
		return fmt.Errorf("marshal client: %w", err)
	}
	_, err = c.ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.tableName),
		Item:      item,
	})
	if err != nil {
		return fmt.Errorf("save client: %w", err)
	}
	return nil
}

// GetClient retrieves a registered client by client_id.
// Returns ErrNotFound if the client does not exist or has expired.
func (c *Client) GetClient(ctx context.Context, clientID string) (*RegisteredClient, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: clientPK(clientID)},
			"SK": &types.AttributeValueMemberS{Value: clientRecordSK},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get client: %w", err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}

	var rec clientRecord
	if err := attributevalue.UnmarshalMap(out.Item, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal client: %w", err)
	}

	if time.Now().Unix() > rec.ExpiresAt {
		return nil, ErrNotFound
	}

	return &RegisteredClient{
		ClientID:    rec.ClientID,
		RedirectURI: rec.RedirectURI,
		IssuedAt:    rec.IssuedAt,
	}, nil
}
