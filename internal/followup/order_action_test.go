package followup

import (
	"testing"
	"time"
)

func TestReady(t *testing.T) {
	due := time.Date(2026, 8, 20, 18, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "one second early stays queued", now: due.Add(-time.Second), want: false},
		{name: "exact due time dispatches", now: due, want: true},
		{name: "late delivery dispatches", now: due.Add(15 * time.Minute), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := OrderAction{DueAt: due}
			if got := Ready(action, tt.now); got != tt.want {
				t.Fatalf("Ready() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewOrderActionAddsCheckoutDelay(t *testing.T) {
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	action := NewOrderAction(Checkout{OrderID: "ord_42", DelayHours: 4}, now)
	want := time.Date(2026, 8, 20, 14, 0, 0, 0, time.UTC)
	if !action.DueAt.Equal(want) {
		t.Fatalf("DueAt = %s, want %s", action.DueAt, want)
	}
}
