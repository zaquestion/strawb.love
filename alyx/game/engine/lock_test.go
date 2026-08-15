package engine

import "testing"

func TestCutLockDeterministicPerSerial(t *testing.T) {
	a := cutLock(3, DefaultPinCount)
	b := cutLock(3, DefaultPinCount)
	c := cutLock(4, DefaultPinCount)
	for i := range a.Pins {
		if a.Pins[i].Target != b.Pins[i].Target {
			t.Fatalf("pin %d: same serial cut different targets (%d vs %d)", i, a.Pins[i].Target, b.Pins[i].Target)
		}
	}
	same := true
	for i := range a.Pins {
		if a.Pins[i].Target != c.Pins[i].Target {
			same = false
		}
	}
	if same {
		t.Fatal("serials 3 and 4 cut identical locks — seeding is broken")
	}
}

func TestCutLockTargetsInRange(t *testing.T) {
	for serial := 1; serial <= 500; serial++ {
		l := cutLock(serial, DefaultPinCount)
		if len(l.Pins) != DefaultPinCount {
			t.Fatalf("serial %d: %d pins, want %d", serial, len(l.Pins), DefaultPinCount)
		}
		for i, p := range l.Pins {
			if p.Target < TargetMin || p.Target > TargetMax {
				t.Fatalf("serial %d pin %d: target %d outside %d..%d", serial, i, p.Target, TargetMin, TargetMax)
			}
			if p.Set {
				t.Fatalf("serial %d pin %d: cut already set", serial, i)
			}
		}
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	e := New()
	e.SetAct(2)
	e.SetParam("pickStrength", 9000)
	e.SetParam("forgiveness", 40)
	e.SetUser("alyx")
	// Rough the state up so the round trip has something to preserve.
	e.PushPin(0, e.Lock.Pins[0].Target)
	e.PushPin(1, 0) // a slip, so hp differs from max
	e.LocksInstalled = 3
	e.Patched = true
	e.LastHookError = "sentry7 crashed mid-pick: nope"

	snap := e.Snapshot()
	r := New()
	if err := r.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := r.Snapshot(); got != snap {
		t.Fatalf("round trip drifted:\n before %s\n after  %s", snap, got)
	}
	// Pin targets must be re-cut identically (they are not in the snapshot).
	for i := range e.Lock.Pins {
		if r.Lock.Pins[i].Target != e.Lock.Pins[i].Target {
			t.Fatalf("pin %d target drifted: %d vs %d", i, r.Lock.Pins[i].Target, e.Lock.Pins[i].Target)
		}
	}
}

func TestRestoreRejectsGarbage(t *testing.T) {
	for _, snap := range []string{"", "{", `{"v":1,"pinCount":0,"serial":1}`, `{"v":1,"pinCount":99,"serial":1}`} {
		e := New()
		before := e.Snapshot()
		if err := e.Restore(snap); err == nil {
			t.Fatalf("restore accepted garbage %q", snap)
		}
		if e.Snapshot() != before {
			t.Fatalf("failed restore of %q mutated the engine", snap)
		}
	}
}

func TestRestoreResumesSerialsPastCurrent(t *testing.T) {
	e := New()
	snap := e.Snapshot()
	r := New()
	if err := r.Restore(snap); err != nil {
		t.Fatalf("restore: %v", err)
	}
	r.NewLock()
	if r.Lock.Serial <= 1 {
		t.Fatalf("restored engine re-cut serial %d — nextSerial did not resume", r.Lock.Serial)
	}
}
