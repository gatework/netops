// Package governance decides whether a change may execute now: change
// class, maintenance windows, change freezes, tickets and approvals.
//
// Gate is a pure function of a Policy and a Request. It returns every
// refusal at once as a stable Reason code, so the operator sees all that
// blocks a change and the CLI can render the reasons in any language. An
// emergency change may bypass windows and freezes, but only with a written
// justification, and the bypasses are recorded in the decision.
package governance
