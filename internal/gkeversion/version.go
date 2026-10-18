// Package gkeversion parses and compares GKE version strings of the form
// "1.35.3-gke.1290000" (as reported by the control plane and by
// node.status.nodeInfo.kubeletVersion, which prefixes a "v").
//
// Comparison is major, minor, patch, then the -gke build number. A version
// with no -gke suffix sorts below the same patch version with one, which is
// the conservative choice: an unrecognised build is treated as not meeting a
// floor rather than meeting it.
package gkeversion

import (
	"fmt"
	"regexp"
	"strconv"
)

// Version is a parsed GKE version.
type Version struct {
	Major, Minor, Patch int
	GKEBuild            int // 0 when the version carries no -gke suffix
	HasGKEBuild         bool
	Raw                 string
}

var pattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-gke\.(\d+))?`)

// Parse reads a GKE version string. A leading "v" and any trailing content
// after the -gke build number are tolerated.
func Parse(s string) (Version, error) {
	m := pattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("not a recognisable GKE version: %q", s)
	}
	v := Version{Raw: s}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		v.GKEBuild, _ = strconv.Atoi(m[4])
		v.HasGKEBuild = true
	}
	return v, nil
}

// MustParse is Parse for constants known to be valid at compile time.
func MustParse(s string) Version {
	v, err := Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

// Compare returns -1, 0 or 1 as v sorts before, equal to, or after other.
func (v Version) Compare(other Version) int {
	for _, pair := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
		{v.GKEBuild, other.GKEBuild},
	} {
		switch {
		case pair[0] < pair[1]:
			return -1
		case pair[0] > pair[1]:
			return 1
		}
	}
	return 0
}

// AtLeast reports whether v meets or exceeds floor.
func (v Version) AtLeast(floor Version) bool { return v.Compare(floor) >= 0 }

func (v Version) String() string {
	if v.Raw != "" {
		return v.Raw
	}
	if v.HasGKEBuild {
		return fmt.Sprintf("%d.%d.%d-gke.%d", v.Major, v.Minor, v.Patch, v.GKEBuild)
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}
