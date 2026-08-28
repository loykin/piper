package execution

import "testing"

func TestCanTransitionExecution(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{StatusAwaitingApproval, StatusQueued, true},
		{StatusAwaitingApproval, StatusCancelled, true},
		{StatusAwaitingApproval, StatusRunning, false},
		{StatusQueued, StatusRunning, true},
		{StatusQueued, StatusCancelled, true},
		{StatusQueued, StatusSucceeded, false},
		{StatusRunning, StatusSucceeded, true},
		{StatusRunning, StatusConflicted, true},
		{StatusRunning, StatusFailed, true},
		{StatusRunning, StatusTimedOut, true},
		{StatusRunning, StatusCancelling, true},
		{StatusRunning, StatusCancelled, true},
		{StatusRunning, StatusAwaitingApproval, false},
		{StatusCancelling, StatusCancelled, true},
		{StatusCancelling, StatusFailed, true},
		{StatusCancelling, StatusRunning, false},
		// terminal states never transition anywhere
		{StatusSucceeded, StatusQueued, false},
		{StatusFailed, StatusQueued, false},
		{StatusCancelled, StatusQueued, false},
		{StatusConflicted, StatusQueued, false},
		{StatusTimedOut, StatusQueued, false},
		// unknown states are never a valid "from"
		{"bogus", StatusQueued, false},
	}
	for _, tc := range cases {
		got := CanTransitionExecution(tc.from, tc.to)
		if got != tc.want {
			t.Errorf("CanTransitionExecution(%q, %q) = %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestIsTerminalExecutionStatus(t *testing.T) {
	terminal := []string{StatusSucceeded, StatusConflicted, StatusFailed, StatusTimedOut, StatusCancelled}
	for _, s := range terminal {
		if !IsTerminalExecutionStatus(s) {
			t.Errorf("IsTerminalExecutionStatus(%q) = false, want true", s)
		}
	}
	nonTerminal := []string{StatusAwaitingApproval, StatusQueued, StatusRunning, StatusCancelling, "bogus"}
	for _, s := range nonTerminal {
		if IsTerminalExecutionStatus(s) {
			t.Errorf("IsTerminalExecutionStatus(%q) = true, want false", s)
		}
	}
}

func TestActiveExecutionStatusesMatchesTerminalComplement(t *testing.T) {
	// Every status is either terminal or in ActiveExecutionStatuses (plus
	// awaiting_approval, which is neither — it hasn't entered the
	// queued/running/cancelling lifecycle recovery scans, so it is
	// deliberately excluded from both).
	active := map[string]bool{}
	for _, s := range ActiveExecutionStatuses {
		active[s] = true
	}
	all := []string{StatusAwaitingApproval, StatusQueued, StatusRunning, StatusCancelling, StatusSucceeded, StatusConflicted, StatusFailed, StatusTimedOut, StatusCancelled}
	for _, s := range all {
		terminal := IsTerminalExecutionStatus(s)
		isActive := active[s]
		if terminal && isActive {
			t.Errorf("%q is both terminal and in ActiveExecutionStatuses", s)
		}
		if !terminal && !isActive && s != StatusAwaitingApproval {
			t.Errorf("%q is neither terminal nor in ActiveExecutionStatuses", s)
		}
	}
}
