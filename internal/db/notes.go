package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/pwntato/notoriousmcp/internal/models"
)

type noteRecord struct {
	PK         string   `dynamodbav:"PK"`
	SK         string   `dynamodbav:"SK"`
	GSI1PK     string   `dynamodbav:"GSI1PK"`
	GSI1SK     string   `dynamodbav:"GSI1SK"`
	ID         string   `dynamodbav:"ID"`
	UserID     string   `dynamodbav:"UserID"`
	Title      string   `dynamodbav:"Title"`
	Tags       []string `dynamodbav:"Tags"`
	S3Key      string   `dynamodbav:"S3Key"`
	Version    int      `dynamodbav:"Version"`
	CreatedAt  string   `dynamodbav:"CreatedAt"`
	ModifiedAt string   `dynamodbav:"ModifiedAt"`
}

// GetNote retrieves note metadata by ID.
func (c *Client) GetNote(ctx context.Context, userID, noteID string) (*models.Note, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: noteSK(noteID)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get note: %w", err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	return noteFromItem(out.Item)
}

// SaveNote upserts note metadata. Version > 1 triggers a conditional write
// (optimistic concurrency). Version == 1 is an unconditional first-write; two
// concurrent first-writes with the same ID will silently overwrite — safe
// because IDs are caller-supplied UUIDs and collisions are astronomically unlikely.
func (c *Client) SaveNote(ctx context.Context, n *models.Note) error {
	modAt := n.ModifiedAt.UTC().Format(isoFormat)
	input := &dynamodb.PutItemInput{
		TableName: aws.String(c.tableName),
		Item: map[string]types.AttributeValue{
			"PK":         &types.AttributeValueMemberS{Value: pk(n.UserID)},
			"SK":         &types.AttributeValueMemberS{Value: noteSK(n.ID)},
			"GSI1PK":     &types.AttributeValueMemberS{Value: gsi1PK(n.UserID, "NOTE")},
			"GSI1SK":     &types.AttributeValueMemberS{Value: gsi1SK(modAt, n.ID)},
			"ID":         &types.AttributeValueMemberS{Value: n.ID},
			"UserID":     &types.AttributeValueMemberS{Value: n.UserID},
			"Title":      &types.AttributeValueMemberS{Value: n.Title},
			"S3Key":      &types.AttributeValueMemberS{Value: n.S3Key},
			"Version":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", n.Version)},
			"CreatedAt":  &types.AttributeValueMemberS{Value: n.CreatedAt.UTC().Format(isoFormat)},
			"ModifiedAt": &types.AttributeValueMemberS{Value: modAt},
			"Tags":       tagsAttr(n.Tags),
		},
	}
	if n.Version > 1 {
		input.ConditionExpression = aws.String("Version = :prev")
		input.ExpressionAttributeValues = map[string]types.AttributeValue{
			":prev": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", n.Version-1)},
		}
	}
	_, err := c.ddb.PutItem(ctx, input)
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrVersionConflict
		}
		return fmt.Errorf("save note: %w", err)
	}
	return nil
}

// DeleteNote removes note metadata.
func (c *Client) DeleteNote(ctx context.Context, userID, noteID string) error {
	_, err := c.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: noteSK(noteID)},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return fmt.Errorf("delete note: %w", err)
	}
	return nil
}

// ListNotes returns note metadata for a user, sorted by ModifiedAt descending.
// If modifiedSince is non-empty (ISO 8601), only notes modified after that time are returned.
func (c *Client) ListNotes(ctx context.Context, userID, modifiedSince string) ([]models.Note, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(c.tableName),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: gsi1PK(userID, "NOTE")},
		},
		ScanIndexForward: aws.Bool(false),
	}
	if modifiedSince != "" {
		input.KeyConditionExpression = aws.String("GSI1PK = :pk AND GSI1SK > :since")
		input.ExpressionAttributeValues[":since"] = &types.AttributeValueMemberS{Value: "MODIFIED#" + modifiedSince}
	}

	return queryNotes(ctx, c.ddb, input)
}

func queryNotes(ctx context.Context, ddb *dynamodb.Client, input *dynamodb.QueryInput) ([]models.Note, error) {
	var notes []models.Note
	paginator := dynamodb.NewQueryPaginator(ddb, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list notes: %w", err)
		}
		for _, item := range page.Items {
			n, err := noteFromItem(item)
			if err != nil {
				return nil, err
			}
			notes = append(notes, *n)
		}
	}
	return notes, nil
}

func noteFromItem(item map[string]types.AttributeValue) (*models.Note, error) {
	var rec noteRecord
	if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal note: %w", err)
	}
	n := &models.Note{
		ID:      rec.ID,
		UserID:  rec.UserID,
		Title:   rec.Title,
		Tags:    rec.Tags,
		S3Key:   rec.S3Key,
		Version: rec.Version,
	}
	var err error
	if n.CreatedAt, err = parseTime(rec.CreatedAt); err != nil {
		return nil, fmt.Errorf("parse note CreatedAt: %w", err)
	}
	if n.ModifiedAt, err = parseTime(rec.ModifiedAt); err != nil {
		return nil, fmt.Errorf("parse note ModifiedAt: %w", err)
	}
	return n, nil
}
