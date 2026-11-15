package replay

import "testing"

func TestSafeVerdicts(t *testing.T) {
	cases := []struct {
		labels map[string]LabelStats
		want   bool
	}{
		{nil, false},
		{map[string]LabelStats{"benign": {Total: 5, BlockedBefore: 0, BlockedAfter: 1}}, false},
		{map[string]LabelStats{"attack": {Total: 5, BlockedBefore: 3, BlockedAfter: 2}}, false},
		{map[string]LabelStats{"attack": {Total: 5, BlockedBefore: 3, BlockedAfter: 5}, "benign": {Total: 9}}, true},
		{map[string]LabelStats{"benign": {Total: 9, BlockedBefore: 1, BlockedAfter: 1}}, true},
	}
	for i, c := range cases {
		if got := (Report{Labels: c.labels}).Safe(); got != c.want {
			t.Errorf("case %d: got %v", i, got)
		}
	}
}
