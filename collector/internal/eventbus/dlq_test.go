package eventbus

import (
	"errors"
	"testing"
)

func TestDLQReasonString(t *testing.T) {
	tests := []struct {
		name   string
		reason error
		want   string
	}{
		{name: "nil reason", reason: nil, want: DLQReasonUnknown},
		{name: "non-nil reason", reason: errors.New("boom"), want: "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DLQReasonString(tt.reason); got != tt.want {
				t.Fatalf("DLQReasonString() = %q, want %q", got, tt.want)
			}
		})
	}
}
