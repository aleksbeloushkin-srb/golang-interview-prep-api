package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// DynamoAPI is the part of the DynamoDB client the store uses.
type DynamoAPI interface {
	GetItem(ctx context.Context, in *dynamodb.GetItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(ctx context.Context, in *dynamodb.PutItemInput, opts ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
}

// Dynamo stores progress in one table: pk = USER#<sub>, sk = PROGRESS.
type Dynamo struct {
	Client DynamoAPI
	Table  string
	Now    func() time.Time
}

func key(userID string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: "USER#" + userID},
		"sk": &types.AttributeValueMemberS{Value: "PROGRESS"},
	}
}

func (d *Dynamo) GetProgress(ctx context.Context, userID string) (Progress, bool, error) {
	out, err := d.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(d.Table),
		Key:            key(userID),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return Progress{}, false, fmt.Errorf("get progress: %w", err)
	}
	if out.Item == nil {
		return Progress{}, false, nil
	}
	data, _ := out.Item["data"].(*types.AttributeValueMemberS)
	ver, _ := out.Item["version"].(*types.AttributeValueMemberN)
	at, _ := out.Item["updatedAt"].(*types.AttributeValueMemberS)
	if data == nil || ver == nil || at == nil {
		return Progress{}, false, errors.New("get progress: malformed item")
	}
	version, err := strconv.ParseInt(ver.Value, 10, 64)
	if err != nil {
		return Progress{}, false, fmt.Errorf("get progress: version: %w", err)
	}
	updated, err := time.Parse(time.RFC3339Nano, at.Value)
	if err != nil {
		return Progress{}, false, fmt.Errorf("get progress: updatedAt: %w", err)
	}
	return Progress{Data: json.RawMessage(data.Value), Version: version, UpdatedAt: updated}, true, nil
}

func (d *Dynamo) PutProgress(ctx context.Context, userID string, data json.RawMessage, version int64) (Progress, error) {
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}
	p := Progress{Data: data, Version: version + 1, UpdatedAt: now().UTC()}
	item := key(userID)
	item["data"] = &types.AttributeValueMemberS{Value: string(data)}
	item["version"] = &types.AttributeValueMemberN{Value: strconv.FormatInt(p.Version, 10)}
	item["updatedAt"] = &types.AttributeValueMemberS{Value: p.UpdatedAt.Format(time.RFC3339Nano)}

	// Optimistic locking: write only if nobody saved since the client loaded.
	in := &dynamodb.PutItemInput{TableName: aws.String(d.Table), Item: item}
	if version == 0 {
		in.ConditionExpression = aws.String("attribute_not_exists(pk)")
	} else {
		in.ConditionExpression = aws.String("version = :v")
		in.ExpressionAttributeValues = map[string]types.AttributeValue{
			":v": &types.AttributeValueMemberN{Value: strconv.FormatInt(version, 10)},
		}
	}
	if _, err := d.Client.PutItem(ctx, in); err != nil {
		var failed *types.ConditionalCheckFailedException
		if errors.As(err, &failed) {
			return Progress{}, ErrConflict
		}
		return Progress{}, fmt.Errorf("put progress: %w", err)
	}
	return p, nil
}
