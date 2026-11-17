package domain

import "testing"

func TestTransitionTable(t *testing.T) {
	states := []State{Backlog, Queued, InProgress, Done, Cancelled}
	allowed := map[[2]State]bool{
		{Queued, InProgress}: true, {InProgress, Done}: true,
		{InProgress, Backlog}: true, {Backlog, Cancelled}: true,
		{Queued, Cancelled}: true, {InProgress, Cancelled}: true,
		{Done, Backlog}: true, {Cancelled, Backlog}: true,
	}
	for _, from := range states {
		for _, to := range states {
			if got := CanTransition(from, to); got != allowed[[2]State{from, to}] {
				t.Errorf("%s -> %s = %v", from, to, got)
			}
		}
	}
	if CanTransition(State("invalid"), Backlog) {
		t.Fatal("invalid state accepted")
	}
}

func TestTextValidation(t *testing.T) {
	for _, value := range []string{"", "  ", string([]byte{0xff})} {
		if ValidateText(value, 1, 120) == nil {
			t.Errorf("accepted %q", value)
		}
	}
	if err := ValidateText("Проект", 1, 6); err != nil {
		t.Fatal(err)
	}
	if ValidateText("1234567", 1, 6) == nil {
		t.Fatal("limit ignored")
	}
}
