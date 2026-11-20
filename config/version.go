package config

import (
	"time"

	"github.com/gatework/netops/evidence"
)

// Backup is one configuration taken from a device, ready to archive.
type Backup struct {
	Device string // device ID
	Text   string
	// Normalized is the digest of the normalized text (Document.Digest),
	// used to tell real changes from timestamp noise.
	Normalized evidence.Digest
	At         time.Time
	Source     string // "backup", "pre-change", "post-change", ...
	Note       string
}

// Version is one archived configuration of a device.
type Version struct {
	// Seq numbers the device's versions from 1.
	Seq int `json:"seq"`
	// At is when the configuration was collected.
	At time.Time `json:"at"`
	// Digest is the digest of the raw text.
	Digest evidence.Digest `json:"digest"`
	// Normalized is the digest of the normalized text.
	Normalized evidence.Digest `json:"normalized"`
	Bytes      int             `json:"bytes"`
	Source     string          `json:"source,omitempty"`
	Note       string          `json:"note,omitempty"`
}

// Archived reports the outcome of archiving a backup.
type Archived struct {
	Version Version `json:"version"`
	// Changed is false when the backup equals the latest version.
	Changed bool `json:"changed"`
	// Previous is the version before, when any.
	Previous *Version `json:"previous,omitempty"`
}
