// Package change models configuration changes as immutable, reviewable
// plans.
//
// A Change[M] is one typed intent over a device model M: it validates
// itself, states whether it is eligible on a given model, whether that
// model already satisfies it, and renders the commands (and their
// rollback) that realize it.
//
// NewPlan binds a change to one device, the fingerprint of the model it was
// planned against and audit metadata. The resulting Plan is what the apply
// package executes: before writing it re-reads the device and either
// proceeds (unchanged since planning), skips (already satisfied, so a re-run
// is idempotent) or refuses (drifted). After writing it verifies that the
// change is satisfied and, for Preserving changes, that nothing else moved.
// Inverse derives the plan that undoes an executed plan.
//
// An Envelope is the portable, tamper-evident form of a plan for review and
// approval; a Codec decodes it back with an explicit table of change kinds.
package change
