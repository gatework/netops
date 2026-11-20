// Package drift describes semantic deviations from an expectation: a
// baseline, the consensus of peer devices, a previous snapshot or the
// operator's intent.
//
// A Deviation is a typed, render-safe record of one difference. It names
// its source, carries display strings that the producer has already masked
// and cites the evidence it was derived from, and it converts to an
// assess.Finding without losing what drifted.
package drift
