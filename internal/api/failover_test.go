package api

import "testing"

func TestPlayLogOnlyCountsAViewersOwnPlayOnce(t *testing.T) {
	s := &Server{}
	if s.plays().claimFailure("ann", "f1") {
		t.Fatal("a file never handed out was reported")
	}
	s.plays().handed("ann", "f1")
	if s.plays().claimFailure("bob", "f1") {
		t.Fatal("another viewer reported ann's play")
	}
	if !s.plays().claimFailure("ann", "f1") {
		t.Fatal("ann's own failure wasn't counted")
	}
	if s.plays().claimFailure("ann", "f1") {
		t.Fatal("the same play was counted twice")
	}
	s.plays().handed("ann", "f1")
	if !s.plays().claimFailure("ann", "f1") {
		t.Fatal("a new play couldn't be reported")
	}
}
