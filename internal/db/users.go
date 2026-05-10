package db

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/pwntato/notoriousmcp/internal/models"
)

type userRecord struct {
	PK               string            `dynamodbav:"PK"`
	SK               string            `dynamodbav:"SK"`
	UserID           string            `dynamodbav:"UserID"`
	Email            string            `dynamodbav:"Email"`
	Name             string            `dynamodbav:"Name"`
	Status           models.UserStatus `dynamodbav:"Status"`
	CreatedAt        string            `dynamodbav:"CreatedAt"`
	StorageUsedBytes int64             `dynamodbav:"StorageUsedBytes"`
	StorageCapBytes  *int64            `dynamodbav:"StorageCapBytes,omitempty"`
	TransferCapBytes *int64            `dynamodbav:"TransferCapBytes,omitempty"`
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
		return nil, ErrNotFound
	}

	var rec userRecord
	if err := attributevalue.UnmarshalMap(out.Item, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal user: %w", err)
	}

	return userFromRecord(&rec)
}

// SaveUser upserts a user profile. RefreshToken is not written by this
// function — use SaveRefreshToken for that. Cap fields (StorageCapBytes,
// TransferCapBytes) are only written when non-nil; use UpdateUserCaps to
// set them. StorageUsedBytes is not written here — use AddStorageUsed.
func (c *Client) SaveUser(ctx context.Context, u *models.User) error {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(u.UserID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		UpdateExpression: aws.String("SET #uid = :uid, #email = :email, #name = :name, #status = :status, #createdAt = if_not_exists(#createdAt, :createdAt), #storageUsed = if_not_exists(#storageUsed, :zero)"),
		ExpressionAttributeNames: map[string]string{
			"#uid":         "UserID",
			"#email":       "Email",
			"#name":        "Name",
			"#status":      "Status",
			"#createdAt":   "CreatedAt",
			"#storageUsed": "StorageUsedBytes",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":uid":       &types.AttributeValueMemberS{Value: u.UserID},
			":email":     &types.AttributeValueMemberS{Value: u.Email},
			":name":      &types.AttributeValueMemberS{Value: u.Name},
			":status":    &types.AttributeValueMemberS{Value: string(u.Status)},
			":createdAt": &types.AttributeValueMemberS{Value: u.CreatedAt.UTC().Format(isoFormat)},
			":zero":      &types.AttributeValueMemberN{Value: "0"},
		},
	})
	if err != nil {
		return fmt.Errorf("save user: %w", err)
	}
	return nil
}

// SaveRefreshToken writes only the refresh token attribute for an existing user.
func (c *Client) SaveRefreshToken(ctx context.Context, userID, token string) error {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		UpdateExpression:    aws.String("SET RefreshToken = :token"),
		ConditionExpression: aws.String("attribute_exists(PK)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":token": &types.AttributeValueMemberS{Value: token},
		},
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return fmt.Errorf("save refresh token: %w", err)
	}
	return nil
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
		return "", ErrNotFound
	}
	attr := out.Item["RefreshToken"]
	if attr == nil {
		return "", ErrNoRefreshToken
	}
	v, ok := attr.(*types.AttributeValueMemberS)
	if !ok {
		return "", ErrNoRefreshToken
	}
	return v.Value, nil
}

// DeleteRefreshToken removes the stored Google refresh token for a user.
// It is a no-op if the user has no stored token. Returns ErrNotFound if the
// user record does not exist.
func (c *Client) DeleteRefreshToken(ctx context.Context, userID string) error {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		UpdateExpression:    aws.String("REMOVE RefreshToken"),
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return fmt.Errorf("delete refresh token: %w", err)
	}
	return nil
}

// DeleteUser removes the user's PROFILE record. Used in tests to clean up
// after each test case. It is a no-op if the user does not exist.
func (c *Client) DeleteUser(ctx context.Context, userID string) error {
	_, err := c.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
	})
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
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
		return fmt.Errorf("update user status: %w", err)
	}
	return nil
}

// ListUsers scans for all user profiles, optionally filtered by status.
// This is a full-table scan — it reads every item in the table regardless of
// the SK = "PROFILE" filter (FilterExpression does not reduce RCUs consumed).
// Acceptable for an admin-only operation at low user counts; add a GSI on SK
// if this becomes a hot path.
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
			u, err := userFromRecord(&rec)
			if err != nil {
				return nil, err
			}
			users = append(users, *u)
		}
	}
	return users, nil
}

// UpdateStorageCap sets or clears the per-user storage cap override. A non-nil
// value sets the cap; nil removes the override (restoring the server default).
// Returns ErrNotFound if the user does not exist.
func (c *Client) UpdateStorageCap(ctx context.Context, userID string, cap *int64) error {
	return updateSingleCap(ctx, c, userID, "StorageCapBytes", "#sc", ":sc", cap, "update storage cap")
}

// UpdateTransferCap sets or clears the per-user monthly transfer cap override.
// A non-nil value sets the cap; nil removes the override.
// Returns ErrNotFound if the user does not exist.
func (c *Client) UpdateTransferCap(ctx context.Context, userID string, cap *int64) error {
	return updateSingleCap(ctx, c, userID, "TransferCapBytes", "#tc", ":tc", cap, "update transfer cap")
}

// updateSingleCap is the shared implementation for UpdateStorageCap and UpdateTransferCap.
// It sets the named attribute when cap is non-nil, or REMOVEs it when nil.
func updateSingleCap(ctx context.Context, c *Client, userID, attr, nameKey, valKey string, cap *int64, opName string) error {
	var expr string
	input := &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	}
	if cap != nil {
		expr = "SET " + nameKey + " = " + valKey
		input.ExpressionAttributeNames = map[string]string{nameKey: attr}
		input.ExpressionAttributeValues = map[string]types.AttributeValue{
			valKey: &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", *cap)},
		}
	} else {
		// REMOVE uses the raw attribute name rather than an expression attribute
		// name. This is safe because attr is hardcoded ("StorageCapBytes" or
		// "TransferCapBytes"), neither of which is a DynamoDB reserved word.
		expr = "REMOVE " + attr
	}
	input.UpdateExpression = aws.String(expr)

	_, err := c.ddb.UpdateItem(ctx, input)
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return fmt.Errorf("%s: %w", opName, err)
	}
	return nil
}

// AddStorageUsed atomically adjusts StorageUsedBytes on the user's PROFILE item
// by delta (positive to add, negative to subtract). Returns ErrNotFound if the
// user does not exist.
func (c *Client) AddStorageUsed(ctx context.Context, userID string, delta int64) error {
	_, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: profileSK()},
		},
		UpdateExpression:    aws.String("ADD StorageUsedBytes :delta"),
		ConditionExpression: aws.String("attribute_exists(PK)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":delta": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", delta)},
		},
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return fmt.Errorf("add storage used: %w", err)
	}
	return nil
}

// transferSK returns the sort key for a per-user monthly transfer record.
func transferSK(month string) string { return "TRANSFER#" + month }

// AddTransferUsed atomically increments BytesOut on the TRANSFER#YYYY-MM item
// for the given user and month, and returns the new total. Sets a 60-day TTL
// on the first write of each month so old records self-clean.
func (c *Client) AddTransferUsed(ctx context.Context, userID, month string, delta int64, ttlUnix int64) (int64, error) {
	out, err := c.ddb.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: transferSK(month)},
		},
		UpdateExpression: aws.String("ADD BytesOut :delta SET #ttl = if_not_exists(#ttl, :ttl)"),
		ExpressionAttributeNames: map[string]string{
			"#ttl": "TTL",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":delta": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", delta)},
			":ttl":   &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", ttlUnix)},
		},
		ReturnValues: types.ReturnValueUpdatedNew,
	})
	if err != nil {
		return 0, fmt.Errorf("add transfer used: %w", err)
	}
	attr, ok := out.Attributes["BytesOut"]
	if !ok {
		return 0, fmt.Errorf("add transfer used: BytesOut not in response")
	}
	n, ok := attr.(*types.AttributeValueMemberN)
	if !ok {
		return 0, fmt.Errorf("add transfer used: unexpected type for BytesOut")
	}
	total, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("add transfer used: parse BytesOut: %w", err)
	}
	return total, nil
}

// GetTransferUsed returns the current BytesOut total for a user in a given month.
// Returns 0 (not ErrNotFound) if no transfer record exists yet.
func (c *Client) GetTransferUsed(ctx context.Context, userID, month string) (int64, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: transferSK(month)},
		},
		ProjectionExpression: aws.String("BytesOut"),
	})
	if err != nil {
		return 0, fmt.Errorf("get transfer used: %w", err)
	}
	if out.Item == nil {
		return 0, nil
	}
	attr, ok := out.Item["BytesOut"]
	if !ok {
		return 0, nil
	}
	n, ok := attr.(*types.AttributeValueMemberN)
	if !ok {
		return 0, nil
	}
	total, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("get transfer used: parse BytesOut: %w", err)
	}
	return total, nil
}

func userFromRecord(rec *userRecord) (*models.User, error) {
	u := &models.User{
		UserID:           rec.UserID,
		Email:            rec.Email,
		Name:             rec.Name,
		Status:           rec.Status,
		StorageUsedBytes: rec.StorageUsedBytes,
		StorageCapBytes:  rec.StorageCapBytes,
		TransferCapBytes: rec.TransferCapBytes,
	}
	var err error
	if u.CreatedAt, err = parseTime(rec.CreatedAt); err != nil {
		return nil, fmt.Errorf("parse user CreatedAt: %w", err)
	}
	return u, nil
}
