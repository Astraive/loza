package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestBucketPreservesAllValues reproduces the defect where Bucket created one
// String Attr per value under the same key; attrsToMap keeps only the last one.
func TestBucketPreservesAllValues(t *testing.T) {
	attr := Bucket("tag", "a", "b", "c")
	if attr.Key != "bucket" {
		t.Fatalf("expected key %q, got %q", "bucket", attr.Key)
	}

	got := attrsToMap([]Attr{attr}, true)
	bucket, ok := got["bucket"].(map[string]any)
	if !ok {
		t.Fatalf("expected bucket to be a map, got %T (%v)", got["bucket"], got["bucket"])
	}
	vals, ok := bucket["tag"].([]string)
	if !ok {
		t.Fatalf("expected tag to be a []string, got %T (%v)", bucket["tag"], bucket["tag"])
	}
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(vals, want) {
		t.Fatalf("expected all bucket values %v, got %v", want, vals)
	}
}

// TestBucketSingleValueStillList verifies the value is consistently a list,
// including when only one value is supplied.
func TestBucketSingleValueStillList(t *testing.T) {
	got := attrsToMap([]Attr{Bucket("user.tier", "pro")}, true)
	bucket := got["bucket"].(map[string]any)
	user := bucket["user"].(map[string]any)
	vals, ok := user["tier"].([]string)
	if !ok || !reflect.DeepEqual(vals, []string{"pro"}) {
		t.Fatalf("expected tier list [pro], got %T (%v)", user["tier"], user["tier"])
	}
}

// TestBucketEncodesAllValues confirms the list survives full event encoding.
func TestBucketEncodesAllValues(t *testing.T) {
	enc := JSONEncoder()
	raw, err := enc.EncodeEvent(nil, &Event{Attrs: []Attr{Bucket("tag", "a", "b", "c")}})
	if err != nil {
		t.Fatalf("encode event: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal encoded event: %v", err)
	}
	attrs := doc["attrs"].(map[string]any)
	bucket := attrs["bucket"].(map[string]any)
	vals, ok := bucket["tag"].([]any)
	if !ok {
		t.Fatalf("expected encoded tag to be an array, got %T (%v)", bucket["tag"], bucket["tag"])
	}
	want := []any{"a", "b", "c"}
	if !reflect.DeepEqual(vals, want) {
		t.Fatalf("expected encoded bucket values %v, got %v", want, vals)
	}
}
