// Package assess is the rule engine for inspections, compliance and
// health checks.
//
// A Rule[M] evaluates one model type M and returns Findings. Each rule
// declares its metadata in Meta: identity, default severity, category, the
// evidence dimensions it requires and its typed parameters with defaults.
// A Catalog holds rules explicitly; there is no global registry.
//
// A Policy, usually decoded from YAML, enables or disables rules and
// overrides severities and parameters. Overrides are checked against the
// declared parameter types when the Engine is built, so a mistyped YAML
// value is an error instead of a silently ignored default.
//
// The Engine skips a rule whose required evidence is not complete
// ("insufficient_evidence") rather than letting it judge on missing data.
// Rules may run in parallel and must not mutate the model.
package assess
