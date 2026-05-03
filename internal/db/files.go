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

type fileRecord struct {
	PK         string `dynamodbav:"PK"`
	SK         string `dynamodbav:"SK"`
	GSI1PK     string `dynamodbav:"GSI1PK"`
	GSI1SK     string `dynamodbav:"GSI1SK"`
	Path       string `dynamodbav:"Path"`
	UserID     string `dynamodbav:"UserID"`
	S3Key      string `dynamodbav:"S3Key"`
	Size       int64  `dynamodbav:"Size"`
	Version    int    `dynamodbav:"Version"`
	CreatedAt  string `dynamodbav:"CreatedAt"`
	ModifiedAt string `dynamodbav:"ModifiedAt"`
}

// GetFile retrieves file metadata by path.
func (c *Client) GetFile(ctx context.Context, userID, path string) (*models.File, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: fileSK(path)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	return fileFromItem(out.Item)
}

// SaveFile upserts file metadata.
func (c *Client) SaveFile(ctx context.Context, f *models.File) error {
	modAt := f.ModifiedAt.UTC().Format(isoFormat)
	input := &dynamodb.PutItemInput{
		TableName: aws.String(c.tableName),
		Item: map[string]types.AttributeValue{
			"PK":         &types.AttributeValueMemberS{Value: pk(f.UserID)},
			"SK":         &types.AttributeValueMemberS{Value: fileSK(f.Path)},
			"GSI1PK":     &types.AttributeValueMemberS{Value: gsi1PK(f.UserID, "FILE")},
			"GSI1SK":     &types.AttributeValueMemberS{Value: gsi1SK(modAt, f.Path)},
			"Path":       &types.AttributeValueMemberS{Value: f.Path},
			"UserID":     &types.AttributeValueMemberS{Value: f.UserID},
			"S3Key":      &types.AttributeValueMemberS{Value: f.S3Key},
			"Size":       &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", f.Size)},
			"Version":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", f.Version)},
			"CreatedAt":  &types.AttributeValueMemberS{Value: f.CreatedAt.UTC().Format(isoFormat)},
			"ModifiedAt": &types.AttributeValueMemberS{Value: modAt},
		},
	}
	if f.Version > 1 {
		input.ConditionExpression = aws.String("Version = :prev")
		input.ExpressionAttributeValues = map[string]types.AttributeValue{
			":prev": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", f.Version-1)},
		}
	}
	_, err := c.ddb.PutItem(ctx, input)
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrVersionConflict
		}
		return fmt.Errorf("save file: %w", err)
	}
	return nil
}

// DeleteFile removes file metadata.
func (c *Client) DeleteFile(ctx context.Context, userID, path string) error {
	_, err := c.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: fileSK(path)},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return fmt.Errorf("delete file: %w", err)
	}
	return nil
}

// ListFiles returns file metadata for a user, optionally filtered to items modified after modifiedSince.
func (c *Client) ListFiles(ctx context.Context, userID, modifiedSince string) ([]models.File, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(c.tableName),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: gsi1PK(userID, "FILE")},
		},
		ScanIndexForward: aws.Bool(false),
	}
	if modifiedSince != "" {
		input.KeyConditionExpression = aws.String("GSI1PK = :pk AND GSI1SK > :since")
		input.ExpressionAttributeValues[":since"] = &types.AttributeValueMemberS{Value: "MODIFIED#" + modifiedSince}
	}

	var files []models.File
	paginator := dynamodb.NewQueryPaginator(c.ddb, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list files: %w", err)
		}
		for _, item := range page.Items {
			f, err := fileFromItem(item)
			if err != nil {
				return nil, err
			}
			files = append(files, *f)
		}
	}
	return files, nil
}

func fileFromItem(item map[string]types.AttributeValue) (*models.File, error) {
	var rec fileRecord
	if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal file: %w", err)
	}
	f := &models.File{
		Path:    rec.Path,
		UserID:  rec.UserID,
		S3Key:   rec.S3Key,
		Size:    rec.Size,
		Version: rec.Version,
	}
	var err error
	if f.CreatedAt, err = parseTime(rec.CreatedAt); err != nil {
		return nil, fmt.Errorf("parse file CreatedAt: %w", err)
	}
	if f.ModifiedAt, err = parseTime(rec.ModifiedAt); err != nil {
		return nil, fmt.Errorf("parse file ModifiedAt: %w", err)
	}
	return f, nil
}
