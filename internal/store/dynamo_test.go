package store

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// fakeDynamo keeps items in a map and evaluates the two conditions the store uses.
type fakeDynamo struct {
	items map[string]map[string]types.AttributeValue
}

func (f *fakeDynamo) id(k map[string]types.AttributeValue) string {
	return k["pk"].(*types.AttributeValueMemberS).Value + "|" + k["sk"].(*types.AttributeValueMemberS).Value
}

func (f *fakeDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return &dynamodb.GetItemOutput{Item: f.items[f.id(in.Key)]}, nil
}

func (f *fakeDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	if f.items == nil {
		f.items = map[string]map[string]types.AttributeValue{}
	}
	id := f.id(in.Item)
	old, exists := f.items[id]
	switch *in.ConditionExpression {
	case "attribute_not_exists(pk)":
		if exists {
			return nil, &types.ConditionalCheckFailedException{}
		}
	case "version = :v":
		want := in.ExpressionAttributeValues[":v"].(*types.AttributeValueMemberN).Value
		if !exists || old["version"].(*types.AttributeValueMemberN).Value != want {
			return nil, &types.ConditionalCheckFailedException{}
		}
	}
	f.items[id] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}

func TestDynamoProgress(t *testing.T) {
	at := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	d := &Dynamo{Client: &fakeDynamo{}, Table: "t", Now: func() time.Time { return at }}
	ctx := context.Background()
	if _, ok, err := d.GetProgress(ctx, "u"); ok || err != nil {
		t.Fatalf("empty: ok=%v err=%v", ok, err)
	}
	p, err := d.PutProgress(ctx, "u", json.RawMessage(`{"a":1}`), 0)
	if err != nil || p.Version != 1 {
		t.Fatalf("first put: %+v %v", p, err)
	}
	if _, err := d.PutProgress(ctx, "u", json.RawMessage(`{"a":2}`), 0); !errors.Is(err, ErrConflict) {
		t.Fatalf("second create: err = %v, want ErrConflict", err)
	}
	if _, err := d.PutProgress(ctx, "u", json.RawMessage(`{"a":2}`), 1); err != nil {
		t.Fatal(err)
	}
	got, ok, err := d.GetProgress(ctx, "u")
	if err != nil || !ok || got.Version != 2 || string(got.Data) != `{"a":2}` || !got.UpdatedAt.Equal(at) {
		t.Fatalf("get: %+v ok=%v err=%v", got, ok, err)
	}
}
