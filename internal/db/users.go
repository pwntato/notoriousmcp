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

type userRecord struct {
	PK           string           `dynamodbav:"PK"`
	SK           string           `dynamodbav:"SK"`
	UserID       string           `dynamodbav:"UserID"`
	Email        string           `dynamodbav:"Email"`
	Name         string           `dynamodbav:"Name"`
	Status       models.UserStatus `dynamodbav:"Status"`
	RefreshToken string           `dynamodbav:"RefreshToken"`
	CreatedAt    string           `dynamodbav:"CreatedAt"`
}

// GetUser retrieves a user profile by user ID.
func (c *Client) GetUser(ctx context.Context, userID string) (*models.User, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	if out.Item == nil {
		return nil, nil
	}

	var rec userRecord
	if err := attributevalue.UnmarshalMap(out.Item, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal user: %w", err)
	}

	return userFromRecord(&rec), nil
}

// SaveUser upserts a user profile. RefreshToken is not written by this
// function — use SaveRefreshToken for that.
func (c *Client) SaveUser(ctx context.Context, u *models.User) error {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(u.UserID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		UpdateExpression: aws.String("SET #uid = :uid, #email = :email, #name = :name, #status = :status, #createdAt = if_not_exists(#createdAt, :createdAt)"),
		ExpressionAttributeNames: map[string]string{
			"#uid":       "UserID",
			"#email":     "Email",
			"#name":      "Name",
			"#status":    "Status",
			"#createdAt": "CreatedAt",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":uid":       &types.AttributeValueMemberS{Value: u.UserID},
			":email":     &types.AttributeValueMemberS{Value: u.Email},
			":name":      &types.AttributeValueMemberS{Value: u.Name},
			":status":    &types.AttributeValueMemberS{Value: string(u.Status)},
			":createdAt": &types.AttributeValueMemberS{Value: u.CreatedAt.UTC().Format(isoFormat)},
		},
	})
	return err
}

// SaveRefreshToken writes only the refresh token attribute for a user.
func (c *Client) SaveRefreshToken(ctx context.Context, userID, token string) error {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		UpdateExpression: aws.String("SET RefreshToken = :token"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":token": &types.AttributeValueMemberS{Value: token},
		},
	})
	return err
}

// LoadRefreshToken reads only the refresh token attribute for a user.
func (c *Client) LoadRefreshToken(ctx context.Context, userID string) (string, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		ProjectionExpression: aws.String("RefreshToken"),
	})
	if err != nil {
		return "", fmt.Errorf("load refresh token: %w", err)
	}
	if out.Item == nil {
		return "", nil
	}
	v, ok := out.Item["RefreshToken"].(*types.AttributeValueMemberS)
	if !ok {
		return "", nil
	}
	return v.Value, nil
}

// UpdateUserStatus sets a user's status field only.
func (c *Client) UpdateUserStatus(ctx context.Context, userID string, status models.UserStatus) error {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		UpdateExpression:    aws.String("SET #status = :status"),
		ConditionExpression: aws.String("attribute_exists(PK)"),
		ExpressionAttributeNames: map[string]string{
			"#status": "Status",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":status": &types.AttributeValueMemberS{Value: string(status)},
		},
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// ListUsers scans for all user profiles, optionally filtered by status.
func (c *Client) ListUsers(ctx context.Context, status *models.UserStatus) ([]models.User, error) {
	input := &dynamodb.ScanInput{
		TableName:        aws.String(c.tableName),
		FilterExpression: aws.String("SK = :sk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":sk": &types.AttributeValueMemberS{Value: profileSK()},
		},
	}
	if status != nil {
		input.FilterExpression = aws.String("SK = :sk AND #status = :status")
		input.ExpressionAttributeNames = map[string]string{"#status": "Status"}
		input.ExpressionAttributeValues[":status"] = &types.AttributeValueMemberS{Value: string(*status)}
	}

	var users []models.User
	paginator := dynamodb.NewScanPaginator(c.ddb, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list users: %w", err)
		}
		for _, item := range page.Items {
			var rec userRecord
			if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
				return nil, fmt.Errorf("unmarshal user: %w", err)
			}
			users = append(users, *userFromRecord(&rec))
		}
	}
	return users, nil
}

func userFromRecord(rec *userRecord) *models.User {
	u := &models.User{
		UserID: rec.UserID,
		Email:  rec.Email,
		Name:   rec.Name,
		Status: rec.Status,
	}
	u.CreatedAt, _ = parseTime(rec.CreatedAt)
	return u
}
