package db

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const isoFormat = time.RFC3339Nano

func parseTime(s string) (time.Time, error) {
	return time.Parse(isoFormat, s)
}

func tagsAttr(tags []string) types.AttributeValue {
	if len(tags) == 0 {
		return &types.AttributeValueMemberL{Value: []types.AttributeValue{}}
	}
	vals := make([]types.AttributeValue, len(tags))
	for i, t := range tags {
		vals[i] = &types.AttributeValueMemberS{Value: t}
	}
	return &types.AttributeValueMemberL{Value: vals}
}
