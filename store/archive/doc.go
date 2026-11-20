// Package archive is the configuration version repository: every backup of
// every device, deduplicated by content.
//
// Layout:
//
//	<root>/objects/<aa>/<sha256>          configuration text, stored once
//	<root>/devices/<name>/index.json      the device's versions, oldest first
//
// Put records a version for each backup. When the normalized digest equals
// the latest version's, nothing new is stored and the result says the
// configuration is unchanged; otherwise the text is stored under its raw
// digest. Versions, Latest and At answer "what changed and when"; Get
// returns the text for diffing or restore. Prune applies a retention
// policy and removes unreferenced objects.
package archive
