package jsonenc

import (
	"math"
	"testing"
)

func TestWriterBuildsNestedJSONAndReusesBuffer(t *testing.T) {
	w := New(nil)
	w.BeginObject()
	w.AppendStringField("message", "hello")
	w.AppendInt64Field("signed", -7)
	w.AppendUint64Field("unsigned", 8)
	w.AppendFloat64Field("ratio", 1.25)
	w.AppendBoolField("enabled", true)
	w.AppendBoolField("disabled", false)
	w.AppendNullField("missing")
	w.AppendKey("items")
	w.BeginArray()
	w.AppendString("first")
	w.AppendString("second")
	w.EndArray()
	w.EndObject()
	want := `{"message":"hello","signed":-7,"unsigned":8,"ratio":1.25,"enabled":true,"disabled":false,"missing":null,"items":["first","second"]}`
	if got := string(w.Bytes()); got != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
	w.Newline()
	if got := string(w.Bytes()); got[len(got)-1] != '\n' {
		t.Fatal("Newline did not append a newline")
	}
	w.Reset()
	if len(w.Bytes()) != 0 {
		t.Fatalf("Reset left %d bytes", len(w.Bytes()))
	}
	w.BeginArray()
	w.AppendRaw([]byte(`{"raw":true}`))
	w.EndArray()
	if got := string(w.Bytes()); got != `[{"raw":true}]` {
		t.Fatalf("raw JSON = %s", got)
	}
}

// TestCompositeArrayElementsUseSep reproduces MASS-0485: composites written as
// array elements must go through the separator state machine instead of
// hand-injected commas. Hand-injected commas leave the array's first flag stuck
// at true, so a later sep-based element at that level is emitted without a
// separator and produces malformed JSON.
func TestCompositeArrayElementsUseSep(t *testing.T) {
	w := New(nil)
	w.BeginObject()
	w.AppendKey("items")
	w.BeginArray()
	w.Sep()
	w.BeginObject()
	w.AppendStringField("name", "first")
	w.EndObject()
	w.Sep()
	w.BeginObject()
	w.AppendStringField("name", "second")
	w.EndObject()
	// A sep-based element after the composites (AppendString inserts its own
	// separator): with hand-injected commas the first flag would still be true
	// and this string would not get a comma.
	w.AppendString("tail")
	w.EndArray()
	w.AppendStringField("after", "ok")
	w.EndObject()

	want := `{"items":[{"name":"first"},{"name":"second"},"tail"],"after":"ok"}`
	if got := string(w.Bytes()); got != want {
		t.Fatalf("JSON = %s, want %s", got, want)
	}
	if len(w.first) != 0 {
		t.Fatalf("first stack not balanced: %v", w.first)
	}

	// Sep must also behave for a bare array of composites with no mixed values.
	w.Reset()
	w.BeginArray()
	for _, name := range []string{"a", "b", "c"} {
		w.Sep()
		w.BeginObject()
		w.AppendStringField("n", name)
		w.EndObject()
	}
	w.EndArray()
	if got, want := string(w.Bytes()), `[{"n":"a"},{"n":"b"},{"n":"c"}]`; got != want {
		t.Fatalf("composite array = %s, want %s", got, want)
	}
}

func TestAppendEscapedStringHandlesJSONAndInvalidUTF8(t *testing.T) {
	input := "quote\" slash\\ newline\n carriage\r tab\t control\x01 <safe> utf8-✓ " + string([]byte{0xff})
	got := string(AppendEscapedString(nil, input))
	want := `"quote\" slash\\ newline\n carriage\r tab\t control\u0001 <safe> utf8-✓ \ufffd"`
	if got != want {
		t.Fatalf("escaped = %s, want %s", got, want)
	}
}

func TestAppendNumbersEncodesSpecialFloatsAsNull(t *testing.T) {
	if got := string(AppendInt64(nil, -123)); got != "-123" {
		t.Fatalf("int = %s", got)
	}
	if got := string(AppendUint64(nil, math.MaxUint64)); got != "18446744073709551615" {
		t.Fatalf("uint = %s", got)
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := string(AppendFloat64(nil, value)); got != "null" {
			t.Fatalf("special float %v = %s, want null", value, got)
		}
	}
}
