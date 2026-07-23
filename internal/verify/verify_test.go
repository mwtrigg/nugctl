package verify

import "testing"

func TestReport_ExitCode(t *testing.T) {
	cases := []struct {
		name string
		r    Report
		want int
	}{
		{"empty", Report{}, 0},
		{"all pass", Report{Checks: []Check{{Status: StatusPass}, {Status: StatusWarn}, {Status: StatusSkip}}}, 0},
		{"one fail", Report{Checks: []Check{{Status: StatusPass}, {Status: StatusFail}}}, 1},
		{"aborted wins over fail", Report{Aborted: true, Checks: []Check{{Status: StatusFail}}}, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.ExitCode(); got != tc.want {
				t.Errorf("ExitCode() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestReport_Counts(t *testing.T) {
	r := Report{Checks: []Check{
		{Status: StatusPass}, {Status: StatusPass}, {Status: StatusFail},
		{Status: StatusWarn}, {Status: StatusSkip},
	}}
	pass, fail, warn, skip := r.Counts()
	if pass != 2 || fail != 1 || warn != 1 || skip != 1 {
		t.Errorf("Counts() = %d,%d,%d,%d, want 2,1,1,1", pass, fail, warn, skip)
	}
}
