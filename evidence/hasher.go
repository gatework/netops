package evidence

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"slices"
	"strconv"
)

// Hasher builds a fingerprint from explicitly named fields. Each field is
// written as a length-prefixed key and value, so distinct field sequences
// never collide by concatenation. The domain separates fingerprints of
// different models and schema versions.
//
// The zero value is not usable; use NewHasher. A Hasher is not safe for
// concurrent use.
type Hasher struct {
	h hash.Hash
}

// NewHasher returns a Hasher for the given domain, such as "wlan-state/v1".
func NewHasher(domain string) *Hasher {
	h := &Hasher{h: sha256.New()}
	h.write("domain", domain)
	return h
}

// String adds a string field.
func (h *Hasher) String(key, value string) *Hasher {
	h.write(key, value)
	return h
}

// Int adds an integer field.
func (h *Hasher) Int(key string, value int64) *Hasher {
	h.write(key, strconv.FormatInt(value, 10))
	return h
}

// Bool adds a boolean field.
func (h *Hasher) Bool(key string, value bool) *Hasher {
	h.write(key, strconv.FormatBool(value))
	return h
}

// Strings adds an ordered list field.
func (h *Hasher) Strings(key string, values []string) *Hasher {
	h.write(key, strconv.Itoa(len(values)))
	for _, v := range values {
		h.write("", v)
	}
	return h
}

// Set adds an unordered set field: values are sorted and deduplicated.
func (h *Hasher) Set(key string, values []string) *Hasher {
	s := slices.Clone(values)
	slices.Sort(s)
	return h.Strings(key, slices.Compact(s))
}

// Sum returns the digest of every field written so far. The Hasher may keep
// receiving fields afterwards.
func (h *Hasher) Sum() Digest { return Digest(hex.EncodeToString(h.h.Sum(nil))) }

func (h *Hasher) write(key, value string) {
	var n [binary.MaxVarintLen64]byte
	for _, s := range [2]string{key, value} {
		_, _ = h.h.Write(n[:binary.PutUvarint(n[:], uint64(len(s)))])
		_, _ = h.h.Write([]byte(s))
	}
}
