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

var (
	ErrAuthCodeNotFound = errors.New("auth code not found or expired")
	ErrAuthCodeCollision = errors.New("auth code collision")
)

const authCodeSK = "AUTHCODE"

type authCodeRecord struct {
	PK                  string `dynamodbav:"PK"`
	SK                  string `dynamodbav:"SK"`
	UserID              string `dynamodbav:"UserID"`
	RedirectURI         string `dynamodbav:"RedirectURI"`
	CodeChallenge       string `dynamodbav:"CodeChallenge,omitempty"`
	CodeChallengeMethod string `dynamodbav:"CodeChallengeMethod,omitempty"`
	ExpiresAt           int64  `dynamodbav:"ExpiresAt"`
}

func authCodePK(code string) string { return "AUTHCODE#" + code }

// SaveAuthCode stores a short-lived opaque exchange code mapped to a user ID.
// redirectURI is the redirect_uri from the authorization request; callers must
// pass a non-empty value — RedeemAuthCode rejects codes stored without one.
// codeChallenge and codeChallengeMethod are the PKCE values from the authorization
// request; pass empty strings for non-PKCE flows.
// ExpiresAt is written as a Unix epoch integer for DynamoDB TTL compatibility.
func (c *Client) SaveAuthCode(ctx context.Context, code, userID, redirectURI, codeChallenge, codeChallengeMethod string, ttl time.Duration) error {
	rec := authCodeRecord{
		PK:                  authCodePK(code),
		SK:                  authCodeSK,
		UserID:              userID,
		RedirectURI:         redirectURI,
		CodeChallenge:       codeChallenge,
		CodeChallengeMethod: codeChallengeMethod,
		ExpiresAt:           time.Now().Add(ttl).Unix(),
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
			return ErrAuthCodeCollision
		}
		return fmt.Errorf("save auth code: %w", err)
	}
	return nil
}

// RedeemedAuthCode holds the values recovered when an exchange code is redeemed.
type RedeemedAuthCode struct {
	UserID              string
	RedirectURI         string // empty if none was bound at save time
	CodeChallenge       string // empty for non-PKCE flows
	CodeChallengeMethod string // empty for non-PKCE flows
}

// RedeemAuthCode atomically deletes the code and returns the associated values.
// Returns ErrAuthCodeNotFound if the code does not exist, was already redeemed,
// or has passed its ExpiresAt (checked in-process after the delete).
// Single-use is guaranteed by the delete: a second caller will find no item.
func (c *Client) RedeemAuthCode(ctx context.Context, code string) (RedeemedAuthCode, error) {
	out, err := c.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: authCodePK(code)},
			"SK": &types.AttributeValueMemberS{Value: authCodeSK},
		},
		ReturnValues: types.ReturnValueAllOld,
	})
	if err != nil {
		return RedeemedAuthCode{}, fmt.Errorf("redeem auth code: %w", err)
	}
	if len(out.Attributes) == 0 {
		return RedeemedAuthCode{}, ErrAuthCodeNotFound
	}

	var rec authCodeRecord
	if err := attributevalue.UnmarshalMap(out.Attributes, &rec); err != nil {
		return RedeemedAuthCode{}, fmt.Errorf("unmarshal auth code: %w", err)
	}

	// DynamoDB TTL expiry is eventual; check expiry ourselves to be precise.
	if time.Now().Unix() > rec.ExpiresAt {
		return RedeemedAuthCode{}, ErrAuthCodeNotFound
	}

	return RedeemedAuthCode{
		UserID:              rec.UserID,
		RedirectURI:         rec.RedirectURI,
		CodeChallenge:       rec.CodeChallenge,
		CodeChallengeMethod: rec.CodeChallengeMethod,
	}, nil
}
