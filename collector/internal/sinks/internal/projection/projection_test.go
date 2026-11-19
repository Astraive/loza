package projection

import (
	"strings"
	"testing"
)

func TestExtractPathSupportsArrayIndices(t *testing.T) {
	doc, err := DecodeObject([]byte(`{"checkpoints":[{"name":"start"},{"name":"end"}]}`))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	got, ok := ExtractPath(doc, "checkpoints[1].name")
	if !ok || got != "end" {
		t.Fatalf("expected checkpoints[1].name=end, got %v (%v)", got, ok)
	}
}

func TestValidatePathRejectsMalformedPaths(t *testing.T) {
	cases := []string{
		"",
		"user..id",
		"user.",
		"a[0].",
		"user[abc]",
		"user[]",
		"user]",
	}
	for _, tc := range cases {
		if err := ValidatePath(tc); err == nil {
			t.Fatalf("expected invalid path error for %q", tc)
		}
	}
}

func TestValidatePathRejectsTrailingDotAfterIndex(t *testing.T) {
	err := ValidatePath("a[0].")
	if err == nil {
		t.Fatal("expected invalid path error for trailing dot after array index")
	}
	if !strings.Contains(err.Error(), "trailing dot") {
		t.Fatalf("expected trailing dot error, got: %v", err)
	}

	doc, dErr := DecodeObject([]byte(`{"a":[{"b":"value"}]}`))
	if dErr != nil {
		t.Fatalf("decode failed: %v", dErr)
	}
	if _, ok := ExtractPath(doc, "a[0]."); ok {
		t.Fatal("expected ExtractPath to reject trailing dot after array index")
	}
}

func TestValidatePathAcceptsMixedSegments(t *testing.T) {
	if err := ValidatePath("events[0].attrs.user.id"); err != nil {
		t.Fatalf("expected valid path, got: %v", err)
	}
}

func TestValidatePathRejectsKeyAdjacentToIndex(t *testing.T) {
	cases := []string{
		"a[0]b",
		"events[0]attrs",
		"checkpoints[1]name",
		"a[0]b.c",
		"a[0].",
	}
	for _, tc := range cases {
		if err := ValidatePath(tc); err == nil {
			t.Fatalf("expected invalid path error for %q", tc)
		}
	}
}

func TestValidatePathAcceptsNestedIndices(t *testing.T) {
	for _, tc := range []string{"matrix[0][1]", "a[0].[1]"} {
		if err := ValidatePath(tc); err != nil {
			t.Fatalf("expected valid path %q, got: %v", tc, err)
		}
	}
}

func TestExtractPathRejectsKeyAdjacentToIndex(t *testing.T) {
	doc, err := DecodeObject([]byte(`{"events":[{"attrs":{"id":"x"}}]}`))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if got, ok := ExtractPath(doc, "events[0]attrs.id"); ok {
		t.Fatalf("expected events[0]attrs.id to be rejected, got %v", got)
	}
	if got, ok := ExtractPath(doc, "events[0].attrs.id"); !ok || got != "x" {
		t.Fatalf("expected events[0].attrs.id=x, got %v (%v)", got, ok)
	}
}
