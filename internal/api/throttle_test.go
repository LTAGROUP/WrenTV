package api

import (
	"testing"
	"time"
)

func TestLoginThrottle(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	th := newLoginThrottle()
	th.now = func() time.Time { return now }

	for i := 0; i < throttlePairFree-1; i++ {
		th.failed("Sam", "10.0.0.1")
		if w := th.wait("sam", "10.0.0.1"); w != 0 {
			t.Fatalf("try %d: waiting %v before the limit", i+1, w)
		}
	}
	th.failed("sam", "10.0.0.1")
	if w := th.wait("SAM ", "10.0.0.1"); w != throttleBase {
		t.Fatalf("wait after the limit = %v, want %v", w, throttleBase)
	}
	// Someone else, and the same name from another address, aren't held up.
	if w := th.wait("alex", "10.0.0.1"); w != 0 {
		t.Fatalf("another user waits %v", w)
	}
	if w := th.wait("sam", "10.0.0.2"); w != 0 {
		t.Fatalf("another address waits %v", w)
	}
	// The cooldown passes, and grows with the next wrong try.
	now = now.Add(throttleBase + time.Second)
	if w := th.wait("sam", "10.0.0.1"); w != 0 {
		t.Fatalf("still waiting %v after the cooldown", w)
	}
	th.failed("sam", "10.0.0.1")
	if w := th.wait("sam", "10.0.0.1"); w != 2*throttleBase {
		t.Fatalf("second cooldown = %v, want %v", w, 2*throttleBase)
	}
	// It never passes the cap.
	for i := 0; i < 40; i++ {
		th.failed("sam", "10.0.0.1")
	}
	if w := th.wait("sam", "10.0.0.1"); w != throttleMax {
		t.Fatalf("capped cooldown = %v, want %v", w, throttleMax)
	}
	// A correct password clears that username from that address.
	th2 := newLoginThrottle()
	for i := 0; i < throttlePairFree-1; i++ {
		th2.failed("sam", "10.0.0.1")
	}
	th2.succeeded("sam", "10.0.0.1")
	th2.failed("sam", "10.0.0.1")
	if w := th2.wait("sam", "10.0.0.1"); w != 0 {
		t.Fatalf("waiting %v after a good sign-in reset the count", w)
	}
}

func TestLoginThrottleManyNamesFromOneAddress(t *testing.T) {
	th := newLoginThrottle()
	for i := 0; i < throttleWideFree; i++ {
		th.failed(string(rune('a'+i)), "10.0.0.9")
	}
	if w := th.wait("someone-new", "10.0.0.9"); w == 0 {
		t.Fatal("an address guessing many usernames isn't slowed")
	}
}
