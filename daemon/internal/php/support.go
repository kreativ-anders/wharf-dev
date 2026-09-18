// Package php knows which PHP versions exist, which are still supported, and
// which are installed on this machine.
package php

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Release is one PHP minor version's published support timeline.
//
// PHP has no formal LTS track: each minor version gets roughly two years of
// active (bug-fix) support followed by two years of security fixes. The
// closest equivalent to "the latest LTS" is therefore the newest release still
// in active support, which is what Recommended returns.
//
// This table is the one piece of the daemon that goes stale with time rather
// than with code. Update it when PHP publishes a new minor version; the dates
// come from https://www.php.net/supported-versions.php.
type Release struct {
	Version       string
	Released      time.Time
	ActiveUntil   time.Time
	SecurityUntil time.Time
}

func date(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic("php: bad date in release table: " + s)
	}
	return t
}

// Releases is ordered oldest first.
var Releases = []Release{
	{"8.1", date("2021-11-25"), date("2023-11-25"), date("2025-12-31")},
	{"8.2", date("2022-12-08"), date("2024-12-31"), date("2026-12-31")},
	{"8.3", date("2023-11-23"), date("2025-12-31"), date("2027-12-31")},
	{"8.4", date("2024-11-21"), date("2026-12-31"), date("2028-12-31")},
	{"8.5", date("2025-11-20"), date("2027-12-31"), date("2029-12-31")},
}

// MinimumForKirby is the oldest version Kirby 4 and 5 run on. Offering
// anything older would serve a Kirby site that cannot boot.
const MinimumForKirby = "8.1"

// Known returns the release entry for a version.
func Known(version string) (Release, bool) {
	for _, r := range Releases {
		if r.Version == version {
			return r, true
		}
	}
	return Release{}, false
}

// Status is how a version stands at a point in time.
type Status string

const (
	// StatusActive is in active support: the version to prefer.
	StatusActive Status = "active"
	// StatusSecurity still receives security fixes.
	StatusSecurity Status = "security"
	// StatusEndOfLife receives nothing.
	StatusEndOfLife Status = "eol"
	// StatusUnreleased is in the table but not out yet.
	StatusUnreleased Status = "unreleased"
	// StatusUnknown is a version the table does not list, such as a build the
	// user compiled themselves.
	StatusUnknown Status = "unknown"
)

// StatusAt reports where a version stands at a given time.
func StatusAt(version string, now time.Time) Status {
	r, ok := Known(version)
	if !ok {
		return StatusUnknown
	}
	switch {
	case now.Before(r.Released):
		return StatusUnreleased
	case now.Before(r.ActiveUntil):
		return StatusActive
	case now.Before(r.SecurityUntil):
		return StatusSecurity
	default:
		return StatusEndOfLife
	}
}

// Recommended is the version to default to when nothing is installed: the
// newest release still in active support. It never returns an unreleased
// version, so a table updated ahead of a release does not point the daemon at
// a build nobody can download yet.
func Recommended(now time.Time) string {
	best := ""
	for _, r := range Releases {
		if StatusAt(r.Version, now) != StatusActive {
			continue
		}
		if best == "" || Less(best, r.Version) {
			best = r.Version
		}
	}
	if best != "" {
		return best
	}
	// INFO: Every listed release has aged out — the table needs updating. Fall back
	// to the newest one that was ever released rather than to nothing.
	for i := len(Releases) - 1; i >= 0; i-- {
		if !now.Before(Releases[i].Released) {
			return Releases[i].Version
		}
	}
	return MinimumForKirby
}

// Preferred picks the default from what is actually installed: the newest
// version in active support, else the newest still receiving security fixes,
// else the newest installed at all. It returns "" when nothing is installed,
// leaving the caller to fall back to Recommended.
func Preferred(installed []string, now time.Time) string {
	byStatus := map[Status][]string{}
	for _, v := range installed {
		byStatus[StatusAt(v, now)] = append(byStatus[StatusAt(v, now)], v)
	}
	for _, s := range []Status{StatusActive, StatusSecurity, StatusUnknown, StatusEndOfLife} {
		if best := newest(byStatus[s]); best != "" {
			return best
		}
	}
	return ""
}

func newest(versions []string) string {
	best := ""
	for _, v := range versions {
		if best == "" || Less(best, v) {
			best = v
		}
	}
	return best
}

// Less orders version strings numerically, so "8.10" sorts above "8.9".
func Less(a, b string) bool {
	as, bs := parts(a), parts(b)
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}

// Sort orders versions oldest first.
func Sort(versions []string) {
	sort.Slice(versions, func(i, j int) bool { return Less(versions[i], versions[j]) })
}

func parts(v string) []int {
	fields := strings.Split(v, ".")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil {
			return out
		}
		out = append(out, n)
	}
	return out
}

// Minor reduces a full version like "8.3.14" to the "8.3" the tool tracks.
func Minor(full string) string {
	f := strings.Split(strings.TrimSpace(full), ".")
	if len(f) < 2 {
		return strings.TrimSpace(full)
	}
	return f[0] + "." + f[1]
}
