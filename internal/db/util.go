package db

import (
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// isoFormat uses RFC3339 (second precision) rather than RFC3339Nano because
// RFC3339Nano omits trailing zeros, producing variable-length fractional seconds
// that don't sort lexicographically in the same order as chronologically.
// Second precision is sufficient for GSI range queries and avoids the edge case.
//
// IMPORTANT: the GSI sort key format (gsi1SK) embeds timestamps formatted with
// this constant. Changing the format would invalidate all existing GSI keys.
const isoFormat = time.RFC3339

func parseTime(s string) (time.Time, error) {
	return time.Parse(isoFormat, s)
}

// tagsAttr always writes an empty list for nil/empty tags rather than omitting
// the attribute. This keeps the schema uniform (Tags always present) at the cost
// of a few extra bytes per item. attribute_not_exists(Tags) will never match.
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
