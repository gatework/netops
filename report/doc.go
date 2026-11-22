// Package report is the common document model of operator reports and its
// renderers: aligned text for terminals, Markdown for tickets and chat,
// CSV for spreadsheets and JSON for machines.
//
// A Document is a title, summary key-values and sections; a section holds
// paragraphs, a table and findings. Findings render as a table with one
// column order everywhere, so reports from fabric, wireless and firemon
// read alike. Every cell is sanitized of terminal control characters, and
// Markdown and CSV escaping are applied by the renderer, never by callers.
package report
