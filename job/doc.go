// Package job runs scheduled operations: nightly backups, periodic
// collection, compliance sweeps.
//
// A Spec is "@every 5m", "@hourly", "@daily", "@weekly" or a five-field
// cron expression ("minute hour day-of-month month day-of-week") in an
// explicit time zone. A Scheduler runs each job on its own timeline: a
// run never overlaps the previous run of the same job, and times missed
// while a run was still going (or the process was down) are not caught
// up, so a restart does not unleash a burst of runs. Every run is
// recorded through the History port, and failures can be published as
// events.
package job
