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

type todoListRecord struct {
	PK         string   `dynamodbav:"PK"`
	SK         string   `dynamodbav:"SK"`
	GSI1PK     string   `dynamodbav:"GSI1PK"`
	GSI1SK     string   `dynamodbav:"GSI1SK"`
	ID         string   `dynamodbav:"ID"`
	UserID     string   `dynamodbav:"UserID"`
	Title      string   `dynamodbav:"Title"`
	Tags       []string `dynamodbav:"Tags"`
	Version    int      `dynamodbav:"Version"`
	CreatedAt  string   `dynamodbav:"CreatedAt"`
	ModifiedAt string   `dynamodbav:"ModifiedAt"`
}

type todoRecord struct {
	PK         string             `dynamodbav:"PK"`
	SK         string             `dynamodbav:"SK"`
	GSI1PK     string             `dynamodbav:"GSI1PK"`
	GSI1SK     string             `dynamodbav:"GSI1SK"`
	ID         string             `dynamodbav:"ID"`
	ListID     string             `dynamodbav:"ListID"`
	UserID     string             `dynamodbav:"UserID"`
	Text       string             `dynamodbav:"Text"`
	Status     models.TodoStatus  `dynamodbav:"Status"`
	DueDate    string             `dynamodbav:"DueDate,omitempty"`
	Tags       []string           `dynamodbav:"Tags"`
	Version    int                `dynamodbav:"Version"`
	CreatedAt  string             `dynamodbav:"CreatedAt"`
	ModifiedAt string             `dynamodbav:"ModifiedAt"`
}

// GetTodoList retrieves a todo list by ID.
func (c *Client) GetTodoList(ctx context.Context, userID, listID string) (*models.TodoList, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: todoListSK(listID)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get todo list: %w", err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	return todoListFromItem(out.Item)
}

// SaveTodoList upserts a todo list.
func (c *Client) SaveTodoList(ctx context.Context, l *models.TodoList) error {
	modAt := l.ModifiedAt.UTC().Format(isoFormat)
	input := &dynamodb.PutItemInput{
		TableName: aws.String(c.tableName),
		Item: map[string]types.AttributeValue{
			"PK":         &types.AttributeValueMemberS{Value: pk(l.UserID)},
			"SK":         &types.AttributeValueMemberS{Value: todoListSK(l.ID)},
			"GSI1PK":     &types.AttributeValueMemberS{Value: gsi1PK(l.UserID, itemTypeTodoList)},
			"GSI1SK":     &types.AttributeValueMemberS{Value: gsi1SK(modAt, l.ID)},
			"ID":         &types.AttributeValueMemberS{Value: l.ID},
			"UserID":     &types.AttributeValueMemberS{Value: l.UserID},
			"Title":      &types.AttributeValueMemberS{Value: l.Title},
			"Version":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", l.Version)},
			"CreatedAt":  &types.AttributeValueMemberS{Value: l.CreatedAt.UTC().Format(isoFormat)},
			"ModifiedAt": &types.AttributeValueMemberS{Value: modAt},
			"Tags":       tagsAttr(l.Tags),
		},
	}
	if l.Version == 1 {
		input.ConditionExpression = aws.String("attribute_not_exists(PK)")
	} else {
		input.ConditionExpression = aws.String("Version = :prev")
		input.ExpressionAttributeValues = map[string]types.AttributeValue{
			":prev": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", l.Version-1)},
		}
	}
	_, err := c.ddb.PutItem(ctx, input)
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrVersionConflict
		}
		return fmt.Errorf("save todo list: %w", err)
	}
	return nil
}

// DeleteTodoList removes a todo list record. Does not cascade-delete todos —
// orphaned todo items remain in DynamoDB and are still reachable via GetTodo
// or ListTodos direct SK queries; they become inaccessible only via
// ListTodoLists. The MCP handler layer is responsible for deleting todos
// before calling this if orphan-free deletion is required.
func (c *Client) DeleteTodoList(ctx context.Context, userID, listID string) error {
	_, err := c.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: todoListSK(listID)},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return fmt.Errorf("delete todo list: %w", err)
	}
	return nil
}

// ListTodoLists returns all todo lists for a user.
func (c *Client) ListTodoLists(ctx context.Context, userID string) ([]models.TodoList, error) {
	input := &dynamodb.QueryInput{
		TableName:              aws.String(c.tableName),
		IndexName:              aws.String("GSI1"),
		KeyConditionExpression: aws.String("GSI1PK = :pk"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": &types.AttributeValueMemberS{Value: gsi1PK(userID, itemTypeTodoList)},
		},
		ScanIndexForward: aws.Bool(false),
	}

	var lists []models.TodoList
	paginator := dynamodb.NewQueryPaginator(c.ddb, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list todo lists: %w", err)
		}
		for _, item := range page.Items {
			l, err := todoListFromItem(item)
			if err != nil {
				return nil, err
			}
			lists = append(lists, *l)
		}
	}
	return lists, nil
}

// GetTodo retrieves a single todo item.
func (c *Client) GetTodo(ctx context.Context, userID, listID, todoID string) (*models.Todo, error) {
	out, err := c.ddb.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: todoSK(listID, todoID)},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("get todo: %w", err)
	}
	if out.Item == nil {
		return nil, ErrNotFound
	}
	return todoFromItem(out.Item)
}

// SaveTodo upserts a todo item.
func (c *Client) SaveTodo(ctx context.Context, t *models.Todo) error {
	modAt := t.ModifiedAt.UTC().Format(isoFormat)
	item := map[string]types.AttributeValue{
		"PK":         &types.AttributeValueMemberS{Value: pk(t.UserID)},
		"SK":         &types.AttributeValueMemberS{Value: todoSK(t.ListID, t.ID)},
		"GSI1PK":     &types.AttributeValueMemberS{Value: gsi1PK(t.UserID, itemTypeTodo)},
		"GSI1SK":     &types.AttributeValueMemberS{Value: gsi1SK(modAt, t.ID)},
		"ID":         &types.AttributeValueMemberS{Value: t.ID},
		"ListID":     &types.AttributeValueMemberS{Value: t.ListID},
		"UserID":     &types.AttributeValueMemberS{Value: t.UserID},
		"Text":       &types.AttributeValueMemberS{Value: t.Text},
		"Status":     &types.AttributeValueMemberS{Value: string(t.Status)},
		"Version":    &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", t.Version)},
		"CreatedAt":  &types.AttributeValueMemberS{Value: t.CreatedAt.UTC().Format(isoFormat)},
		"ModifiedAt": &types.AttributeValueMemberS{Value: modAt},
		"Tags":       tagsAttr(t.Tags),
	}
	if t.DueDate != nil {
		item["DueDate"] = &types.AttributeValueMemberS{Value: t.DueDate.UTC().Format(isoFormat)}
	}

	input := &dynamodb.PutItemInput{TableName: aws.String(c.tableName), Item: item}
	if t.Version == 1 {
		input.ConditionExpression = aws.String("attribute_not_exists(PK)")
	} else {
		input.ConditionExpression = aws.String("Version = :prev")
		input.ExpressionAttributeValues = map[string]types.AttributeValue{
			":prev": &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", t.Version-1)},
		}
	}
	_, err := c.ddb.PutItem(ctx, input)
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrVersionConflict
		}
		return fmt.Errorf("save todo: %w", err)
	}
	return nil
}

// DeleteTodo removes a todo item.
func (c *Client) DeleteTodo(ctx context.Context, userID, listID, todoID string) error {
	_, err := c.ddb.DeleteItem(ctx, &dynamodb.DeleteItemInput{
		TableName: aws.String(c.tableName),
		Key: map[string]types.AttributeValue{
			"PK": &types.AttributeValueMemberS{Value: pk(userID)},
			"SK": &types.AttributeValueMemberS{Value: todoSK(listID, todoID)},
		},
		ConditionExpression: aws.String("attribute_exists(PK)"),
	})
	if err != nil {
		var cfe *types.ConditionalCheckFailedException
		if errors.As(err, &cfe) {
			return ErrNotFound
		}
		return fmt.Errorf("delete todo: %w", err)
	}
	return nil
}

// ListTodos returns todos for a list, optionally filtered by status or modified since a timestamp.
// listID must be non-empty; callers are responsible for validating it before calling.
func (c *Client) ListTodos(ctx context.Context, userID, listID, modifiedSince string, status *models.TodoStatus) ([]models.Todo, error) {
	if listID == "" {
		return nil, fmt.Errorf("listID must not be empty")
	}
	input := &dynamodb.QueryInput{
		TableName:              aws.String(c.tableName),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: pk(userID)},
			":prefix": &types.AttributeValueMemberS{Value: "TODO#" + listID + "#"},
		},
		ScanIndexForward: aws.Bool(false),
	}

	if modifiedSince != "" {
		// Trade-off: GSI1SK encodes modifiedAt + todoID but not listID, so we can't
		// scope the range query to a single list via the key. We query all todos for
		// the user modified since the timestamp and filter by ListID post-read.
		// This is acceptable at low todo volume; revisit if it becomes a hot path.
		input = &dynamodb.QueryInput{
			TableName:              aws.String(c.tableName),
			IndexName:              aws.String("GSI1"),
			KeyConditionExpression: aws.String("GSI1PK = :pk AND GSI1SK > :since"),
			FilterExpression:       aws.String("ListID = :listID"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":     &types.AttributeValueMemberS{Value: gsi1PK(userID, itemTypeTodo)},
				":since":  &types.AttributeValueMemberS{Value: "MODIFIED#" + modifiedSince},
				":listID": &types.AttributeValueMemberS{Value: listID},
			},
			ScanIndexForward: aws.Bool(false),
		}
	}

	if status != nil {
		if input.ExpressionAttributeNames == nil {
			input.ExpressionAttributeNames = map[string]string{}
		}
		input.ExpressionAttributeNames["#status"] = "Status"
		input.ExpressionAttributeValues[":status"] = &types.AttributeValueMemberS{Value: string(*status)}
		if input.FilterExpression != nil {
			input.FilterExpression = aws.String(*input.FilterExpression + " AND #status = :status")
		} else {
			input.FilterExpression = aws.String("#status = :status")
		}
	}

	var todos []models.Todo
	paginator := dynamodb.NewQueryPaginator(c.ddb, input)
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list todos: %w", err)
		}
		for _, item := range page.Items {
			t, err := todoFromItem(item)
			if err != nil {
				return nil, err
			}
			todos = append(todos, *t)
		}
	}
	return todos, nil
}

func todoListFromItem(item map[string]types.AttributeValue) (*models.TodoList, error) {
	var rec todoListRecord
	if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal todo list: %w", err)
	}
	l := &models.TodoList{
		ID:      rec.ID,
		UserID:  rec.UserID,
		Title:   rec.Title,
		Tags:    rec.Tags,
		Version: rec.Version,
	}
	var err error
	if l.CreatedAt, err = parseTime(rec.CreatedAt); err != nil {
		return nil, fmt.Errorf("parse todo list CreatedAt: %w", err)
	}
	if l.ModifiedAt, err = parseTime(rec.ModifiedAt); err != nil {
		return nil, fmt.Errorf("parse todo list ModifiedAt: %w", err)
	}
	return l, nil
}

func todoFromItem(item map[string]types.AttributeValue) (*models.Todo, error) {
	var rec todoRecord
	if err := attributevalue.UnmarshalMap(item, &rec); err != nil {
		return nil, fmt.Errorf("unmarshal todo: %w", err)
	}
	t := &models.Todo{
		ID:      rec.ID,
		ListID:  rec.ListID,
		UserID:  rec.UserID,
		Text:    rec.Text,
		Status:  rec.Status,
		Tags:    rec.Tags,
		Version: rec.Version,
	}
	var err error
	if t.CreatedAt, err = parseTime(rec.CreatedAt); err != nil {
		return nil, fmt.Errorf("parse todo CreatedAt: %w", err)
	}
	if t.ModifiedAt, err = parseTime(rec.ModifiedAt); err != nil {
		return nil, fmt.Errorf("parse todo ModifiedAt: %w", err)
	}
	if rec.DueDate != "" {
		dd, err := parseTime(rec.DueDate)
		if err != nil {
			return nil, fmt.Errorf("parse todo DueDate: %w", err)
		}
		t.DueDate = &dd
	}
	return t, nil
}
