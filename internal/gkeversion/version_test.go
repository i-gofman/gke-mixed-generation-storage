package gkeversion

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in                       string
		major, minor, patch, bld int
		hasBuild                 bool
	}{
		{"1.35.3-gke.1290000", 1, 35, 3, 1290000, true},
		{"v1.34.1-gke.2541000", 1, 34, 1, 2541000, true},
		{"1.30.3-gke.1451000", 1, 30, 3, 1451000, true},
		{"v1.33.0", 1, 33, 0, 0, false},
	}
	for _, c := range cases {
		got, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", c.in, err)
		}
		if got.Major != c.major || got.Minor != c.minor || got.Patch != c.patch ||
			got.GKEBuild != c.bld || got.HasGKEBuild != c.hasBuild {
			t.Errorf("Parse(%q) = %+v", c.in, got)
		}
	}
	if _, err := Parse("not-a-version"); err == nil {
		t.Error("expected error for unparseable version")
	}
}

func TestAtLeast(t *testing.T) {
	dynamicFloor := MustParse("1.35.3-gke.1290000")
	topologyFloor := MustParse("1.34.1-gke.2541000")

	cases := []struct {
		node  string
		floor Version
		want  bool
	}{
		// Exactly on the floor meets it.
		{"1.35.3-gke.1290000", dynamicFloor, true},
		// A lower gke build on the same patch does not.
		{"1.35.3-gke.1280000", dynamicFloor, false},
		// A higher patch does, even with a lower build number.
		{"1.35.4-gke.1000", dynamicFloor, true},
		// The classic mixed-fleet case: node pool behind the control plane.
		{"1.33.4-gke.9999999", topologyFloor, false},
		{"1.34.1-gke.2541000", topologyFloor, true},
		{"1.36.0-gke.100", topologyFloor, true},
	}
	for _, c := range cases {
		v := MustParse(c.node)
		if got := v.AtLeast(c.floor); got != c.want {
			t.Errorf("%s AtLeast %s = %v, want %v", c.node, c.floor, got, c.want)
		}
	}
}
