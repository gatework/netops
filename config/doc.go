// Package config models device configuration text at the syntax level.
//
// A Dialect describes how a family of CLIs writes its configuration:
// comment and separator lines, block terminators, lines that change on
// every save (timestamps, byte counts) and lines that carry secrets. A
// Document parses a configuration once under a dialect into an indented
// tree, a normalized text that ignores volatile lines, and a digest of that
// text, so that two backups of an unchanged device compare equal.
//
// The package knows syntax only. What a command means belongs to the
// consuming domain module.
package config
