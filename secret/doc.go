// Package secret holds credentials and other sensitive strings in a type
// that never prints, logs or serializes its value.
//
// A Secret reveals its value only through Reveal, so a credential cannot
// leak through %v, slog, JSON or an error message by accident. A Redactor
// removes known secret values from text, such as a command line written to
// a journal.
package secret
