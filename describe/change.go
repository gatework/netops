package describe

import (
	"fmt"
	"maps"

	"github.com/gatework/netops/change"
	"github.com/gatework/netops/iface"
)

// Kind is the change kind of Change.
const Kind change.Kind = "describe.interfaces"

// Item sets the description of one interface.
type Item struct {
	Interface   string `json:"interface"`
	Description string `json:"description"`
}

// Change sets interface descriptions on one device. Its rollback restores
// the descriptions the device had when the change was rendered.
type Change struct {
	Platform string `json:"platform"`
	Items    []Item `json:"items"`
}

var (
	_ change.Change[Model]     = Change{}
	_ change.Preserving[Model] = Change{}
)

// Decoder decodes Change in a change.Codec of Model.
func Decoder() change.Decoder[Model] { return change.DecodeAs[Model, Change](Kind) }

// Kind returns Kind.
func (Change) Kind() change.Kind { return Kind }

// Validate checks the change on its own.
func (c Change) Validate() error {
	p, err := Lookup(c.Platform)
	if err != nil {
		return err
	}
	if !p.Writable() {
		return fmt.Errorf("%w: %s descriptions are read-only", ErrUnsupported, c.Platform)
	}
	if len(c.Items) == 0 {
		return fmt.Errorf("%w: no interfaces", ErrInvalid)
	}
	seen := map[string]bool{}
	for _, it := range c.Items {
		k := iface.Key(it.Interface)
		switch {
		case !iface.Safe(it.Interface):
			return fmt.Errorf("%w: interface %q", ErrInvalid, it.Interface)
		case !ValidDescription(it.Description):
			return fmt.Errorf("%w: description %q", ErrInvalid, it.Description)
		case seen[k]:
			return fmt.Errorf("%w: interface %s twice", ErrInvalid, it.Interface)
		}
		seen[k] = true
	}
	return nil
}

// Eligible requires the platform to match and every interface to exist.
func (c Change) Eligible(m Model) error {
	if m.Platform != c.Platform {
		return fmt.Errorf("%w: change for %s, device is %s", ErrInvalid, c.Platform, m.Platform)
	}
	for _, it := range c.Items {
		if _, ok := m.Ports[iface.Key(it.Interface)]; !ok {
			return fmt.Errorf("%w: interface %s is not configured", ErrInvalid, it.Interface)
		}
	}
	return nil
}

// Satisfied reports whether every interface has its description.
func (c Change) Satisfied(m Model) bool {
	for _, it := range c.Items {
		if m.Ports[iface.Key(it.Interface)].Description != it.Description {
			return false
		}
	}
	return true
}

// Render enters configuration mode once, sets every description and
// leaves; the rollback restores the current descriptions of m.
func (c Change) Render(m Model) (change.Script, error) {
	if err := c.Validate(); err != nil {
		return change.Script{}, err
	}
	s := *builtin[c.Platform].syntax
	do := []string{s.Enter}
	undo := []string{s.Enter}
	for _, it := range c.Items {
		port := m.Ports[iface.Key(it.Interface)]
		name := port.Name
		if name == "" {
			name = it.Interface
		}
		do = append(do, s.block(name, s.Set+it.Description)...)
		if port.Description == "" {
			undo = append(undo, s.block(name, s.Clear)...)
		} else {
			undo = append(undo, s.block(name, s.Set+port.Description)...)
		}
	}
	return change.Script{
		Commands: change.Commands(append(do, s.Leave)...),
		Rollback: change.Commands(append(undo, s.Leave)...),
	}, nil
}

// Preserved fails when a port outside the change changed its description
// or disappeared.
func (c Change) Preserved(before, after Model) error {
	mine := map[string]bool{}
	for _, it := range c.Items {
		mine[iface.Key(it.Interface)] = true
	}
	other := maps.Clone(before.Ports)
	maps.DeleteFunc(other, func(k string, _ Port) bool { return mine[k] })
	for k, p := range other {
		if q, ok := after.Ports[k]; !ok || q.Description != p.Description {
			return fmt.Errorf("describe: %s changed outside the change", p.Name)
		}
	}
	return nil
}
