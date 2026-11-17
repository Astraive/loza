package loadgen

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRunRejectsNonPositiveBatchSize(t *testing.T) {
	for _, batchSize := range []int{0, -1} {
		t.Run(fmt.Sprint(batchSize), func(t *testing.T) {
			_, err := Run(context.Background(), RunnerConfig{
				Events:    1,
				Workers:   1,
				BatchSize: batchSize,
				BodySize:  1,
			})
			if err == nil {
				t.Fatal("Run() error = nil, want non-positive batch size error")
			}
			if !strings.Contains(err.Error(), "batch size must be greater than zero") {
				t.Fatalf("Run() error = %q, want batch size validation error", err)
			}
		})
	}
}
