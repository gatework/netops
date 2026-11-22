// Package event is the vocabulary of operational events and the bus that
// delivers them: a configuration changed out of band, a compliance rule was
// violated, a change executed or ended uncertain, a collection failed.
//
// Events carry codes, digests and structured fields, never device output
// or credentials, so they can be forwarded to chat, ticketing or SIEM
// systems. Each event has a content-derived ID, so a receiver can drop
// duplicates when a tool re-publishes after a retry.
//
// A Bus fans events out to Sinks through optional Filters. A failing sink
// never prevents delivery to the others, and publishing never changes the
// outcome of the operation that produced the event.
package event
