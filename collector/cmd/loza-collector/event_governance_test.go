package main

import "testing"

func TestRedactObjectRedactsSecretValuesInArrays(t *testing.T) {
	blocked := map[string]struct{}{}
	payload := map[string]any{
		"tokens": []any{
			"sk-abcdefghijklmnop",
			"not-a-secret",
			map[string]any{"nested": "sk-1111111111111111"},
			[]any{"sk-2222222222222222"},
		},
	}

	if !redactObject(payload, blocked, true) {
		t.Fatal("expected redactObject to report a modification")
	}

	tokens := payload["tokens"].([]any)
	if tokens[0] != "[REDACTED]" {
		t.Fatalf("expected top-level secret in array to be redacted, got %#v", tokens[0])
	}
	if tokens[1] != "not-a-secret" {
		t.Fatalf("expected non-secret array element to remain unchanged, got %#v", tokens[1])
	}
	nested := tokens[2].(map[string]any)
	if nested["nested"] != "[REDACTED]" {
		t.Fatalf("expected secret in map nested in array to be redacted, got %#v", nested["nested"])
	}
	nestedArr := tokens[3].([]any)
	if nestedArr[0] != "[REDACTED]" {
		t.Fatalf("expected secret in nested array to be redacted, got %#v", nestedArr[0])
	}
}

func TestRedactObjectRedactsSecretValuesInMaps(t *testing.T) {
	blocked := map[string]struct{}{}
	payload := map[string]any{
		"api_key": "sk-abcdefghijklmnop",
		"message": "hello world",
	}

	if !redactObject(payload, blocked, true) {
		t.Fatal("expected redactObject to report a modification")
	}
	if payload["api_key"] != "[REDACTED]" {
		t.Fatalf("expected secret map value to be redacted, got %#v", payload["api_key"])
	}
	if payload["message"] != "hello world" {
		t.Fatalf("expected non-secret map value to remain unchanged, got %#v", payload["message"])
	}
}

func TestRedactObjectLeavesSecretsWhenScanDisabled(t *testing.T) {
	blocked := map[string]struct{}{}
	payload := map[string]any{
		"tokens": []any{"sk-abcdefghijklmnop"},
	}

	if redactObject(payload, blocked, false) {
		t.Fatal("expected no modification when secret scanning is disabled")
	}
	tokens := payload["tokens"].([]any)
	if tokens[0] != "sk-abcdefghijklmnop" {
		t.Fatalf("expected secret to remain when scanning disabled, got %#v", tokens[0])
	}
}
