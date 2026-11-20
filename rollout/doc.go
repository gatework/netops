// Package rollout schedules a fleet change into waves and decides when to
// stop.
//
// Schedule orders devices by tier (for example access before distribution
// before core), optionally starts with a canary, keeps partitions such as
// sites apart, and never puts two devices with the same anti-affinity key
// (an HA pair, a vPC pair, two controllers of one cluster) in the same
// wave. A Tracker applies the stop policy as waves finish: by default the
// rollout halts after the first wave that is not fully verified.
package rollout
