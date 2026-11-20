package change

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/gatework/netops/device"
	"github.com/gatework/netops/evidence"
)

// SchemaVersion is the version of the Envelope document.
const SchemaVersion = 1

// Envelope is the portable form of a plan for review, approval and later
// execution. Digest covers every other field, so any edit is detected on
// decode. Sensitive commands are stored in full: envelopes are private
// files and must be handled like credentials.
type Envelope struct {
	Schema   int             `json:"schema"`
	Meta     Meta            `json:"meta"`
	Target   device.Device   `json:"target"`
	Kind     Kind            `json:"kind"`
	Change   json.RawMessage `json:"change"`
	Baseline evidence.Digest `json:"baseline"`
	Restore  evidence.Digest `json:"restore,omitempty"`
	Script   Script          `json:"script"`
	Digest   evidence.Digest `json:"digest"`
}

// Envelope encodes p.
func (p *Plan[M]) Envelope() (Envelope, error) {
	body, err := json.Marshal(p.change)
	if err != nil {
		return Envelope{}, fmt.Errorf("change: encode %s: %w", p.change.Kind(), err)
	}
	e := Envelope{
		Schema: SchemaVersion, Meta: p.meta, Target: p.target.Clone(), Kind: p.change.Kind(),
		Change: body, Baseline: p.baseline, Restore: p.restore, Script: p.script.clone(),
	}
	if e.Digest, err = e.sum(); err != nil {
		return Envelope{}, err
	}
	return e, nil
}

// Digest returns the tamper-evident digest of the plan.
func (p *Plan[M]) Digest() (evidence.Digest, error) {
	e, err := p.Envelope()
	return e.Digest, err
}

// sum hashes the canonical JSON of the envelope without its digest.
func (e Envelope) sum() (evidence.Digest, error) {
	e.Digest = ""
	var compact bytes.Buffer
	if err := json.Compact(&compact, e.Change); err != nil {
		return "", fmt.Errorf("%w: change body: %w", ErrInvalid, err)
	}
	e.Change = compact.Bytes()
	body, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("change: encode envelope: %w", err)
	}
	return evidence.DigestOf(body), nil
}

// Codec decodes envelopes of model M with an explicit table of change
// kinds. It is immutable and safe for concurrent use.
type Codec[M any] struct {
	fp    Fingerprint[M]
	kinds map[Kind]func([]byte) (Change[M], error)
}

// Decoder decodes the JSON body of one change kind.
type Decoder[M any] struct {
	Kind   Kind
	Decode func([]byte) (Change[M], error)
}

// DecodeAs returns a Decoder that unmarshals into C.
func DecodeAs[M any, C Change[M]](kind Kind) Decoder[M] {
	return Decoder[M]{Kind: kind, Decode: func(b []byte) (Change[M], error) {
		var c C
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("%w: %s body: %w", ErrInvalid, kind, err)
		}
		return c, nil
	}}
}

// NewCodec builds a codec for the fingerprint and kinds.
func NewCodec[M any](fp Fingerprint[M], decoders ...Decoder[M]) (*Codec[M], error) {
	if fp == nil {
		return nil, fmt.Errorf("%w: nil fingerprint", ErrInvalid)
	}
	c := &Codec[M]{fp: fp, kinds: map[Kind]func([]byte) (Change[M], error){}}
	for _, d := range decoders {
		if !d.Kind.Valid() || d.Decode == nil {
			return nil, fmt.Errorf("%w: decoder %q", ErrInvalid, d.Kind)
		}
		if _, dup := c.kinds[d.Kind]; dup {
			return nil, fmt.Errorf("%w: duplicate decoder %s", ErrInvalid, d.Kind)
		}
		c.kinds[d.Kind] = d.Decode
	}
	return c, nil
}

// Decode verifies the envelope digest and schema, decodes the change and
// rebuilds the plan exactly as reviewed: the script is taken from the
// envelope, not re-rendered, so what executes is what was approved.
func (c *Codec[M]) Decode(body []byte) (*Plan[M], error) {
	var e Envelope
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("%w: envelope: %w", ErrInvalid, err)
	}
	if e.Schema != SchemaVersion {
		return nil, fmt.Errorf("%w: envelope schema %d", ErrInvalid, e.Schema)
	}
	sum, err := e.sum()
	if err != nil {
		return nil, err
	}
	if sum != e.Digest {
		return nil, fmt.Errorf("%w: envelope digest mismatch", ErrInvalid)
	}
	decode, ok := c.kinds[e.Kind]
	if !ok {
		return nil, fmt.Errorf("%w: unknown change kind %q", ErrInvalid, e.Kind)
	}
	ch, err := decode(e.Change)
	if err != nil {
		return nil, err
	}
	if err := ch.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := e.Meta.validate(); err != nil {
		return nil, err
	}
	target := e.Target.Normalize()
	if err := target.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := e.Script.validate(); err != nil {
		return nil, err
	}
	if !e.Baseline.Valid() || (e.Restore != "" && !e.Restore.Valid()) {
		return nil, fmt.Errorf("%w: envelope digests", ErrInvalid)
	}
	return &Plan[M]{
		meta: e.Meta, target: target, change: ch, fp: c.fp,
		baseline: e.Baseline, script: e.Script.clone(), restore: e.Restore,
	}, nil
}
