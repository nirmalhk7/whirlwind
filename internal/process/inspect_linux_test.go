package process

import "testing"

func TestLinuxProcessUserUsesKernelCredentials(t *testing.T) {
	for _, test := range []struct {
		status string
		match  bool
		bad    bool
	}{
		{"Name:\tworker\nUid:\t1000\t1000\t1000\t1000\n", true, false},
		{"Uid:\t1000\t0\t0\t0\n", true, false},
		{"Uid:\t0\t0\t0\t0\n", false, false},
		{"Uid:\t1000\tinvalid\t0\t0\n", false, true},
		{"Name:\tworker\n", false, true},
	} {
		match, err := linuxProcessUser(test.status, 1000)
		if match != test.match || (err != nil) != test.bad {
			t.Errorf("linuxProcessUser(%q) = %v, %v", test.status, match, err)
		}
	}
}
