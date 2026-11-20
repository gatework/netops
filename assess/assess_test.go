package assess_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gatework/netops/assess"
	"github.com/gatework/netops/evidence"
)

type model struct{ Loss float64 }

func lossRule() assess.Rule[model] {
	return assess.Check(assess.Meta{
		ID: "test.loss", Title: "loss", Severity: assess.SeverityMedium, Category: assess.CategoryRuntime,
		Requires: []evidence.Dimension{"aps"}, Params: []assess.Param{assess.Number("max", 1)},
	}, func(in assess.Input[model]) []assess.Finding {
		if in.Model.Loss > in.Params.Number("max") {
			return []assess.Finding{{Subject: assess.NewSubject("ap", "ap1"), Detail: fmt.Sprint(in.Model.Loss)}}
		}
		return nil
	})
}

func engine(t *testing.T, policy string, rules ...assess.Rule[model]) *assess.Engine[model] {
	t.Helper()
	cat, err := assess.NewCatalog(rules...)
	if err != nil {
		t.Fatal(err)
	}
	p, err := assess.DecodePolicy([]byte(policy))
	if err != nil {
		t.Fatal(err)
	}
	e, err := assess.NewEngine(cat, p, assess.Options{Workers: 4})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func complete() evidence.Coverage {
	c, _ := evidence.NewCoverage(map[evidence.Dimension]evidence.Status{"aps": evidence.StatusComplete})
	return c
}

func TestEngine_Run(t *testing.T) {
	t.Parallel()
	e := engine(t, "rules:\n  test.loss:\n    severity: high\n    params: {max: 2}\n", lossRule())
	a, err := e.Run(t.Context(), model{Loss: 3}, complete())
	if err != nil || len(a.Findings) != 1 || a.Findings[0].Severity != assess.SeverityHigh || a.Findings[0].Title != "loss" {
		t.Fatalf("findings %+v %v", a.Findings, err)
	}
	a, _ = e.Run(t.Context(), model{Loss: 1.5}, complete())
	if len(a.Findings) != 0 {
		t.Error("override ignored")
	}
	a, _ = e.Run(t.Context(), model{Loss: 9}, evidence.Coverage{})
	if a.Rules[0].State != assess.RuleSkipped || a.Rules[0].Reason != assess.ReasonInsufficientEvidence || !a.Incomplete() {
		t.Errorf("missing evidence: %+v", a.Rules[0])
	}
}

func TestNewEngine_rejectsBadPolicy(t *testing.T) {
	t.Parallel()
	cat, _ := assess.NewCatalog(lossRule())
	for name, policy := range map[string]string{
		"string for number": "rules:\n  test.loss:\n    params: {max: high}\n",
		"unknown param":     "rules:\n  test.loss:\n    params: {maximum: 2}\n",
		"unknown rule":      "rules:\n  test.nope: {}\n",
	} {
		p, err := assess.DecodePolicy([]byte(policy))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := assess.NewEngine(cat, p, assess.Options{}); !errors.Is(err, assess.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := assess.DecodePolicy([]byte("rulez: {}")); err == nil {
		t.Error("unknown field accepted")
	}
}

func TestEngine_defectiveRules(t *testing.T) {
	t.Parallel()
	meta := func(id assess.RuleID) assess.Meta {
		return assess.Meta{ID: id, Title: "t", Severity: assess.SeverityLow, Category: assess.CategoryRuntime}
	}
	e := engine(t, "",
		assess.NewRule(meta("t.panic"), func(context.Context, assess.Input[model]) ([]assess.Finding, error) { panic("bug") }),
		assess.Check(meta("t.param"), func(in assess.Input[model]) []assess.Finding { _ = in.Params.Text("nope"); return nil }),
		assess.NewRule(meta("t.err"), func(context.Context, assess.Input[model]) ([]assess.Finding, error) { return nil, errors.New("x") }),
	)
	a, err := e.Run(t.Context(), model{}, complete())
	if err != nil {
		t.Fatal(err)
	}
	want := []assess.Reason{assess.ReasonPanic, assess.ReasonUndeclaredParam, assess.ReasonError}
	for i, o := range a.Rules {
		if o.State != assess.RuleFailed || o.Reason != want[i] {
			t.Errorf("%s = %+v", o.Rule, o)
		}
	}
}

func FuzzDecodePolicy(f *testing.F) {
	f.Add([]byte("rules:\n  test.loss:\n    params: {max: 2}\n"))
	cat, _ := assess.NewCatalog(lossRule())
	f.Fuzz(func(t *testing.T, body []byte) {
		p, err := assess.DecodePolicy(body)
		if err != nil {
			return
		}
		_, _ = assess.NewEngine(cat, p, assess.Options{})
	})
}

func ExampleEngine_Run() {
	cat, _ := assess.NewCatalog(lossRule())
	e, _ := assess.NewEngine(cat, assess.Policy{}, assess.Options{})
	cov, _ := evidence.NewCoverage(map[evidence.Dimension]evidence.Status{"aps": evidence.StatusComplete})
	a, _ := e.Run(context.Background(), model{Loss: 5}, cov)
	for _, f := range a.Findings {
		fmt.Println(f.Rule, f.Severity, f.Subject)
	}
	// Output: test.loss medium ap:ap1
}
