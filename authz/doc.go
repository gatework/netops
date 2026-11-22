// Package authz decides who may do what on which devices.
//
// A Policy binds actors to roles; a role grants permissions (view,
// collect, backup, plan, approve, deploy, rollback, admin) on the devices
// its scope selects. Authorize answers one question with a reason when
// the answer is no, so a CLI can say exactly what is missing. There is no
// implicit default: a policy that grants nothing denies everything unless
// it explicitly sets AllowAll, which suits single-operator tools.
package authz
