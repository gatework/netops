// Package failure is the stable, vendor-neutral vocabulary of operation
// failures.
//
// Reports, journals and refresh status record a Code instead of an error
// text, so they never carry device output, credentials or transport
// details. Adapters attach a Code at the boundary with New or Wrap; Classify
// recovers the codes from any error chain.
package failure
