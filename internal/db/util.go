package db

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// isoFormat uses RFC3339 (second precision) rather than RFC3339Nano because
// RFC3339Nano omits trailing zeros, producing variable-length fractional seconds
// that don't sort lexicographically in the same order as chronologically.
// Second precision is sufficient for GSI range queries and avoids the edge case.
const isoFormat = time.RFC3339

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
