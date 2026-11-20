package change_test

import (
	"errors"
	"testing"

	"github.com/gatework/netops/change"
	"github.com/gatework/netops/evidence"
)

// bounce is an imperative change: it succeeds when the uptime restarted.
type bounce struct{}

func (bounce) Kind() change.Kind       { return "test.bounce" }
func (bounce) Validate() error         { return nil }
func (bounce) Eligible(int) error      { return nil }
func (bounce) Satisfied(int) bool      { return false }
func (bounce) Effective(b, a int) bool { return a < b }
func (bounce) Render(int) (change.Script, error) {
	return change.Script{Commands: change.Commands("shutdown ; no shutdown")}, nil
}

func TestPlan_Verify_effect(t *testing.T) {
	t.Parallel()
	fp := func(int) evidence.Digest { return evidence.NewHasher("uptime").Sum() }
	p, err := change.NewPlan(meta, sw1, 100, change.Change[int](bounce{}), fp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Verify(100, 3); err != nil {
		t.Errorf("restarted uptime not effective: %v", err)
	}
	if _, err := p.Verify(100, 101); !errors.Is(err, change.ErrUnverified) {
		t.Errorf("unchanged uptime verified: %v", err)
	}
}
