package snapshot_test

import "github.com/gatework/netops/evidence"

func sha(s string) string { return string(evidence.DigestString(s)) }
