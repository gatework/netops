package authz

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/gatework/netops/device"
)

// Permission is one kind of operation.
type Permission string

// Permissions. Admin implies every other permission.
const (
	PermView     Permission = "view"     // read reports and stored evidence
	PermCollect  Permission = "collect"  // log in and run read-only commands
	PermBackup   Permission = "backup"   // archive configurations
	PermPlan     Permission = "plan"     // create change plans
	PermApprove  Permission = "approve"  // approve plans of others
	PermDeploy   Permission = "deploy"   // execute plans
	PermRollback Permission = "rollback" // undo executed plans
	PermAdmin    Permission = "admin"
)

var known = []Permission{PermView, PermCollect, PermBackup, PermPlan, PermApprove, PermDeploy, PermRollback, PermAdmin}

// Valid reports whether p is known.
func (p Permission) Valid() bool { return slices.Contains(known, p) }

// ErrDenied reports a refused operation; *DeniedError wraps it.
var ErrDenied = errors.New("authz: denied")

// DeniedError explains a refusal.
type DeniedError struct {
	Actor      string
	Permission Permission
	Device     string // empty for fleet-wide operations
	Reason     string // "no_role", "no_permission", "out_of_scope"
}

func (d *DeniedError) Error() string {
	if d.Device == "" {
		return fmt.Sprintf("authz: %s may not %s (%s)", d.Actor, d.Permission, d.Reason)
	}
	return fmt.Sprintf("authz: %s may not %s %s (%s)", d.Actor, d.Permission, d.Device, d.Reason)
}

// Unwrap makes errors.Is(err, ErrDenied) true.
func (d *DeniedError) Unwrap() error { return ErrDenied }

// Role grants permissions on the devices its scope selects.
type Role struct {
	Name        string       `yaml:"name" json:"name"`
	Permissions []Permission `yaml:"permissions" json:"permissions"`
	// Scope is device selector clauses; empty means every device.
	Scope []string `yaml:"scope" json:"scope,omitempty"`

	selector device.Selector
}

// Policy is the authorization policy. Build it with Decode or Compile.
type Policy struct {
	// AllowAll grants every permission to everyone; set it explicitly for
	// single-operator use.
	AllowAll bool   `yaml:"allow_all" json:"allow_all,omitempty"`
	Roles    []Role `yaml:"roles" json:"roles"`
	// Bindings maps an actor to role names.
	Bindings map[string][]string `yaml:"bindings" json:"bindings"`

	byName map[string]*Role
}

// Decode parses a YAML policy strictly and compiles it.
func Decode(body []byte) (Policy, error) {
	var p Policy
	dec := yaml.NewDecoder(bytes.NewReader(body))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return Policy{}, fmt.Errorf("authz: decode: %w", err)
	}
	return Compile(p)
}

// Compile validates roles and bindings and returns the policy ready for
// use; Decode calls it.
func Compile(p Policy) (Policy, error) {
	err := compile(&p)
	return p, err
}

func compile(p *Policy) error {
	p.byName = map[string]*Role{}
	for i := range p.Roles {
		r := &p.Roles[i]
		if r.Name == "" || p.byName[r.Name] != nil {
			return fmt.Errorf("authz: role %d: missing or duplicate name %q", i, r.Name)
		}
		for _, perm := range r.Permissions {
			if !perm.Valid() {
				return fmt.Errorf("authz: role %s: unknown permission %q", r.Name, perm)
			}
		}
		sel, err := device.ParseSelector(r.Scope...)
		if err != nil {
			return fmt.Errorf("authz: role %s: %w", r.Name, err)
		}
		r.selector = sel
		p.byName[r.Name] = r
	}
	for _, actor := range slices.Sorted(maps.Keys(p.Bindings)) {
		for _, name := range p.Bindings[actor] {
			if p.byName[name] == nil {
				return fmt.Errorf("authz: actor %s is bound to unknown role %q", actor, name)
			}
		}
	}
	return nil
}

func (r *Role) grants(p Permission) bool {
	return slices.Contains(r.Permissions, PermAdmin) || slices.Contains(r.Permissions, p)
}

// Authorize allows actor to perform p on d, or returns a *DeniedError.
func (p Policy) Authorize(actor string, perm Permission, d device.Device) error {
	if p.AllowAll {
		return nil
	}
	roles := p.roles(actor)
	deny := func(reason string) error {
		return &DeniedError{Actor: actor, Permission: perm, Device: d.ID, Reason: reason}
	}
	if len(roles) == 0 {
		return deny("no_role")
	}
	granted := false
	for _, r := range roles {
		if !r.grants(perm) {
			continue
		}
		granted = true
		if r.selector.Match(d) {
			return nil
		}
	}
	if granted {
		return deny("out_of_scope")
	}
	return deny("no_permission")
}

// Permits reports whether actor holds perm on at least some devices, for
// fleet-wide operations such as viewing reports.
func (p Policy) Permits(actor string, perm Permission) bool {
	if p.AllowAll {
		return true
	}
	return slices.ContainsFunc(p.roles(actor), func(r *Role) bool { return r.grants(perm) })
}

// Filter returns the devices actor may perform perm on, and the refused
// ones.
func (p Policy) Filter(actor string, perm Permission, devices []device.Device) (allowed []device.Device, denied []error) {
	for _, d := range devices {
		if err := p.Authorize(actor, perm, d); err != nil {
			denied = append(denied, err)
			continue
		}
		allowed = append(allowed, d)
	}
	return allowed, denied
}

// roles returns the compiled roles of actor.
func (p Policy) roles(actor string) []*Role {
	var out []*Role
	for _, name := range p.Bindings[actor] {
		if r := p.byName[name]; r != nil {
			out = append(out, r)
		}
	}
	return out
}
