package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var ErrAuthCodeNotFound = errors.New("auth code not found or expired")

type authCodeRecord struct {
	PK        string `dynamodbav:"PK"`
	SK        string `dynamodbav:"SK"`
	UserID    string `dynamodbav:"UserID"`
	ExpiresAt int64  `dynamodbav:"ExpiresAt"`
}

func authCodePK(code string) string { return "AUTHCODE#" + code }
func authCodeSK() string            { return "AUTHCODE" }

// SaveAuthCode stores a short-lived opaque exchange code mapped to a user ID.
// ExpiresAt is written as a Unix epoch integer for DynamoDB TTL compatibility.
func (c *Client) SaveAuthCode(ctx context.Context, code, userID string, ttl time.Duration) error {
	rec := authCodeRecord{
		PK:        authCodePK(code),
		SK:        authCodeSK(),
		UserID:    userID,
		ExpiresAt: time.Now().Add(ttl).Unix(),
	}
	item, err := attributevalue.MarshalMap(rec)
	if err != nil {
		return fmt.Errorf("marshal auth code: %w", err)
	}
	_, err = c.ddb.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:           aws.String(c.tableName),
		Item:                item,
		ConditionExpression: aws.String("attribute_not_exists(PK)"),
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			// Code collision — callers should generate a new code and retry.
			return fmt.Errorf("auth code already exists: %w", ErrAuthCodeNotFound)
		}
		return fmt.Errorf("save auth code: %w", err)
	}
	return nil
}

// RedeemAuthCode atomically deletes the code and returns the associated user ID.
// Returns ErrAuthCodeNotFound if the code does not exist, was already redeemed,
// or has passed its ExpiresAt (checked in-process after the delete).
// Single-use is guaranteed by the delete: a second caller will find no item.
func (c *Client) RedeemAuthCode(ctx context.Context, code string) (string, error) {
	out, err := c.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: authCodePK(code)},
			"SK": &types.AttributeValueMemberS{Value: authCodeSK()},
		},
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		return "", fmt.Errorf("redeem auth code: %w", err)
	}
	if len(out.Attributes) == 0 {
		return "", ErrAuthCodeNotFound
	}

	var rec authCodeRecord
	if err := attributevalue.UnmarshalMap(out.Attributes, &rec); err != nil {
		return "", fmt.Errorf("unmarshal auth code: %w", err)
	}

	// DynamoDB TTL expiry is eventual; check expiry ourselves to be precise.
	if time.Now().Unix() > rec.ExpiresAt {
		return "", ErrAuthCodeNotFound
	}

	return rec.UserID, nil
}
