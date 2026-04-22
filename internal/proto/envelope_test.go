package proto

import "testing"

func TestPIDZero(t *testing.T) {
	t.Parallel()

	zero := PID{}
	if !zero.Zero() {
		t.Fatal("empty PID must report Zero() == true")
	}

	nonZero := PID{Node: "n1", Path: "/a"}
	if nonZero.Zero() {
		t.Fatal("PID{Node:\"n1\",Path:\"/a\"} must report Zero() == false")
	}

	// A PID with only Node set is not zero.
	nodeOnly := PID{Node: "n1"}
	if nodeOnly.Zero() {
		t.Fatal("PID with non-empty Node must report Zero() == false")
	}

	// A PID with only Path set is not zero.
	pathOnly := PID{Path: "/a"}
	if pathOnly.Zero() {
		t.Fatal("PID with non-empty Path must report Zero() == false")
	}
}

func TestPIDString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pid  PID
		want string
	}{
		// Path starts with "/" by convention, so Node+Path = "n/a".
		{PID{Node: "n", Path: "/a"}, "n/a"},
		{PID{Node: "node1", Path: "/actors/foo"}, "node1/actors/foo"},
		{PID{}, "-"},
	}
	for _, tt := range tests {
		got := tt.pid.String()
		if got != tt.want {
			t.Errorf("PID%+v.String() = %q; want %q", tt.pid, got, tt.want)
		}
	}
}
