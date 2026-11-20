package failure_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/gatework/netops/failure"
)

func TestClassify(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want []failure.Code
	}{
		{"nil", nil, nil},
		{"unknown is internal", errors.New("boom"), []failure.Code{failure.CodeInternal}},
		{"wrapped code", fmt.Errorf("x: %w", failure.New(failure.CodeAuth, "login")), []failure.Code{failure.CodeAuth}},
		{"context", fmt.Errorf("x: %w", context.DeadlineExceeded), []failure.Code{failure.CodeTimeout}},
		{"joined", errors.Join(failure.New(failure.CodeRejected, "w"), context.Canceled), []failure.Code{failure.CodeRejected, failure.CodeCancelled}},
		{"code stops descent", failure.Wrap(failure.CodeStorage, "j", context.Canceled), []failure.Code{failure.CodeStorage}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := failure.Classify(tc.err); !slices.Equal(got, tc.want) {
				t.Errorf("Classify = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCancelled(t *testing.T) {
	t.Parallel()
	if failure.Cancelled(context.DeadlineExceeded) || !failure.Cancelled(fmt.Errorf("x: %w", context.Canceled)) {
		t.Error("timeout and cancel are not told apart")
	}
}

func TestCode_text(t *testing.T) {
	t.Parallel()
	var c failure.Code
	if err := c.UnmarshalText([]byte("nonsense")); err == nil {
		t.Error("unknown code accepted")
	}
	if err := c.UnmarshalText([]byte("timeout")); err != nil || c != failure.CodeTimeout {
		t.Errorf("decode = %q, %v", c, err)
	}
	if _, err := failure.Code("nope").MarshalText(); err == nil {
		t.Error("unknown code encoded")
	}
}

func ExampleClassify() {
	err := fmt.Errorf("collect 192.0.2.1: %w", failure.Wrap(failure.CodeAuth, "connect", errors.New("permission denied")))
	fmt.Println(failure.Classify(err), err)
	// Output: [auth_failed] collect 192.0.2.1: connect: auth_failed
}
