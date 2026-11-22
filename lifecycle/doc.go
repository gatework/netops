// Package lifecycle checks the software a fleet runs against a version
// policy: the target release, the minimum acceptable release, releases
// that must not run (advisories, known defects), and the vendor's
// end-of-sale and end-of-support dates.
//
// Versions of the common network operating systems compare segment by
// segment, numbers numerically and words case-insensitively, so
// "9.3(9)" < "9.3(10)", "17.9.4" < "17.9.4a", "21.4R3" < "21.4R3-S5" and
// "7.1.070, Release 6728P05" < "7.1.070, Release 6728P06".
//
// A Policy is an ordered list of rules matched by device selector; the
// first matching rule decides. Evaluate reports every problem of a device
// and the worst one, and converts to assess findings for reports.
package lifecycle
