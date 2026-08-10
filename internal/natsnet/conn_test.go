package natsnet

import "testing"

func TestTransitionActiveStatusPreservesTerminalState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		status         Status
		drainRequested bool
		closeRequested bool
	}{
		{name: "draining", status: StatusDraining, drainRequested: true},
		{name: "close requested", status: StatusClosed, closeRequested: true},
		{name: "closed callback", status: StatusClosed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			conn := &Conn{
				drainRequested: test.drainRequested,
				closeRequested: test.closeRequested,
			}
			conn.status.Store(uint32(test.status))
			if conn.transitionActiveStatus(StatusConnected) {
				t.Fatal("transitionActiveStatus() 覆盖了已提交终态")
			}
			if got := conn.Status(); got != test.status {
				t.Fatalf("Status() = %v, want %v", got, test.status)
			}
		})
	}
}

func TestTransitionActiveStatusMovesBetweenLiveStates(t *testing.T) {
	t.Parallel()

	conn := &Conn{}
	conn.status.Store(uint32(StatusConnecting))
	if !conn.transitionActiveStatus(StatusReconnecting) {
		t.Fatal("transitionActiveStatus() 拒绝了非终态转换")
	}
	if got := conn.Status(); got != StatusReconnecting {
		t.Fatalf("Status() = %v, want %v", got, StatusReconnecting)
	}
}
