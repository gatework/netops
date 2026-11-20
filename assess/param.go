package assess

import (
	"fmt"
	"math"
	"regexp"
	"slices"
)

// ParamKind is the declared type of a rule parameter.
type ParamKind string

// Parameter kinds.
const (
	ParamNumber  ParamKind = "number"
	ParamInteger ParamKind = "integer"
	ParamBool    ParamKind = "bool"
	ParamText    ParamKind = "text"
	ParamList    ParamKind = "list" // a list of strings
)

// Param declares one rule parameter and its default. Construct it with
// Number, Integer, Bool, Text or List.
type Param struct {
	name  string
	kind  ParamKind
	value value
}

type value struct {
	num  float64
	b    bool
	str  string
	list []string
}

var paramName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Number declares a floating-point parameter.
func Number(name string, def float64) Param {
	return Param{name: name, kind: ParamNumber, value: value{num: def}}
}

// Integer declares an integer parameter.
func Integer(name string, def int) Param {
	return Param{name: name, kind: ParamInteger, value: value{num: float64(def)}}
}

// Bool declares a boolean parameter.
func Bool(name string, def bool) Param {
	return Param{name: name, kind: ParamBool, value: value{b: def}}
}

// Text declares a string parameter.
func Text(name, def string) Param { return Param{name: name, kind: ParamText, value: value{str: def}} }

// List declares a list-of-strings parameter.
func List(name string, def ...string) Param {
	return Param{name: name, kind: ParamList, value: value{list: slices.Clone(def)}}
}

// Name returns the parameter name.
func (p Param) Name() string { return p.name }

// Kind returns the parameter kind.
func (p Param) Kind() ParamKind { return p.kind }

// override converts a decoded policy value to p's kind. YAML and JSON
// decoders produce int, int64, uint64, float64, bool, string and []any.
func (p Param) override(raw any) (value, error) {
	bad := func() (value, error) {
		return value{}, fmt.Errorf("%w: parameter %s wants %s, got %T", ErrInvalid, p.name, p.kind, raw)
	}
	switch p.kind {
	case ParamNumber, ParamInteger:
		f, ok := number(raw)
		if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return bad()
		}
		if p.kind == ParamInteger && f != math.Trunc(f) {
			return bad()
		}
		return value{num: f}, nil
	case ParamBool:
		b, ok := raw.(bool)
		if !ok {
			return bad()
		}
		return value{b: b}, nil
	case ParamText:
		s, ok := raw.(string)
		if !ok {
			return bad()
		}
		return value{str: s}, nil
	case ParamList:
		items, ok := raw.([]any)
		if !ok {
			if ss, isStrings := raw.([]string); isStrings {
				return value{list: slices.Clone(ss)}, nil
			}
			return bad()
		}
		out := make([]string, len(items))
		for i, it := range items {
			s, isString := it.(string)
			if !isString {
				return bad()
			}
			out[i] = s
		}
		return value{list: out}, nil
	}
	return bad()
}

func number(raw any) (float64, bool) {
	switch v := raw.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case uint64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

// Params are the effective parameter values a rule sees: its declared
// defaults with policy overrides applied. Reading an undeclared parameter,
// or one of another kind, returns the zero value and fails the rule run.
type Params struct {
	decl   map[string]Param
	misuse *string
}

// Number returns a number parameter.
func (p Params) Number(name string) float64 {
	v, _ := p.get(name, ParamNumber)
	return v.num
}

// Integer returns an integer parameter.
func (p Params) Integer(name string) int {
	v, _ := p.get(name, ParamInteger)
	return int(v.num)
}

// Bool returns a boolean parameter.
func (p Params) Bool(name string) bool {
	v, _ := p.get(name, ParamBool)
	return v.b
}

// Text returns a string parameter.
func (p Params) Text(name string) string {
	v, _ := p.get(name, ParamText)
	return v.str
}

// List returns a copy of a list parameter.
func (p Params) List(name string) []string {
	v, _ := p.get(name, ParamList)
	return slices.Clone(v.list)
}

func (p Params) get(name string, kind ParamKind) (value, bool) {
	d, ok := p.decl[name]
	if !ok || d.kind != kind {
		if p.misuse != nil && *p.misuse == "" {
			*p.misuse = name
		}
		return value{}, false
	}
	return d.value, true
}
