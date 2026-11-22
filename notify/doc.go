// Package notify delivers operational events outside the tool: an
// append-only JSON Lines file for log shippers and SIEM collectors, a
// signed webhook for chat, ticketing and automation platforms, and a
// line-per-event writer for terminals.
//
// Webhook bodies are signed with HMAC-SHA256 over the exact bytes sent
// (header X-Netops-Signature: sha256=<hex>), so receivers can reject
// forged or altered events. Delivery is bounded: a per-attempt timeout and
// a small number of retries with backoff on network errors and 5xx
// answers; 4xx answers are not retried. Plain HTTP is refused unless the
// caller allows it explicitly.
package notify
