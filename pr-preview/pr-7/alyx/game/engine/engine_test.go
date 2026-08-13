package engine

import (
	"math/rand"
	"testing"
)

// rig builds an engine with pin 0's target forced to a known value so window
// tables can speak in absolute forces.
func rig(target int) *Engine {
	e := New()
	e.Lock.Pins[0].Target = target
	return e
}

func TestPushPinWindows(t *testing.T) {
	const target = 50
	tests := []struct {
		name        string
		forgiveness int
		force       int
		wantResult  string
		wantAlmost  bool
	}{
		{"dead center", 1, 50, ResultSet, true},
		{"window edge low", 1, 49, ResultSet, true},
		{"window edge high", 1, 51, ResultSet, true},
		{"one past high is overset", 1, 52, ResultOverset, true},
		{"one past low is slip", 1, 48, ResultSlip, true},
		{"almost edge low", 1, 42, ResultSlip, true}, // |42-50| = 8 = forgiveness+AlmostSlack
		{"almost edge high", 1, 58, ResultOverset, true},
		{"past almost low", 1, 41, ResultSlip, false},
		{"past almost high", 1, 59, ResultOverset, false},
		{"nowhere close", 1, 0, ResultSlip, false},
		{"wide window catches 30", 40, 30, ResultSet, true},
		{"wide window edge", 40, 90, ResultSet, true},
		{"wide window overset", 40, 91, ResultOverset, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := rig(target)
			e.Params.Forgiveness = tc.forgiveness
			e.Params.PickStrength = 100 // hp headroom: these cases are about the window, not the break
			e.Pick = Pick{HP: 100, MaxHP: 100}
			out, _ := e.PushPin(0, tc.force)
			if out.Result != tc.wantResult {
				t.Fatalf("force %d: result %q, want %q", tc.force, out.Result, tc.wantResult)
			}
			if out.Almost != tc.wantAlmost {
				t.Fatalf("force %d: almost %v, want %v", tc.force, out.Almost, tc.wantAlmost)
			}
		})
	}
}

func TestPushCostAndNoCostPaths(t *testing.T) {
	e := rig(50)
	if out, _ := e.PushPin(0, 0); out.Result != ResultSlip || e.Pick.HP != RiggedPickStrength-1 {
		t.Fatalf("slip should chip 1 hp: out=%+v hp=%d", out, e.Pick.HP)
	}
	if out, _ := e.PushPin(0, 50); out.Result != ResultSet || e.Pick.HP != RiggedPickStrength-1 {
		t.Fatalf("set must be free: out=%+v hp=%d", out, e.Pick.HP)
	}
	if out, _ := e.PushPin(0, 50); out.Result != ResultAlready || e.Pick.HP != RiggedPickStrength-1 {
		t.Fatalf("re-push of a set pin must be a cost-free 'already': out=%+v hp=%d", out, e.Pick.HP)
	}
	if out, _ := e.PushPin(-1, 50); out.Result != ResultAlready {
		t.Fatalf("out-of-range pin must be a no-op, got %+v", out)
	}
	if out, _ := e.PushPin(99, 50); out.Result != ResultAlready {
		t.Fatalf("out-of-range pin must be a no-op, got %+v", out)
	}
}

func TestOversetDropsOneSetPin(t *testing.T) {
	e := rig(50)
	e.Params.Forgiveness = 1
	e.Pick = Pick{HP: 100, MaxHP: 100}
	e.Lock.Pins[1].Set = true
	e.Lock.Pins[3].Set = true
	out, _ := e.PushPin(0, 100) // way past 50+1: jam
	if out.Result != ResultOverset {
		t.Fatalf("want overset, got %+v", out)
	}
	if len(out.PinsDropped) != 1 {
		t.Fatalf("want exactly one dropped pin, got %v", out.PinsDropped)
	}
	d := out.PinsDropped[0]
	if d != 1 && d != 3 {
		t.Fatalf("dropped pin %d was never set", d)
	}
	if e.Lock.Pins[d].Set {
		t.Fatalf("dropped pin %d still reads set", d)
	}
	setCount := 0
	for _, p := range e.Lock.Pins {
		if p.Set {
			setCount++
		}
	}
	if setCount != 1 {
		t.Fatalf("exactly one pin should survive the jam, got %d", setCount)
	}
}

func TestOversetWithNoSetPinsDropsNothing(t *testing.T) {
	e := rig(50)
	out, _ := e.PushPin(0, 100)
	if out.Result != ResultOverset || len(out.PinsDropped) != 0 {
		t.Fatalf("nothing was set, nothing should drop: %+v", out)
	}
}

func TestPickBreaksAtZeroAndStaysBroken(t *testing.T) {
	e := rig(50)
	var lastEvent string
	for range RiggedPickStrength {
		_, lastEvent = e.PushPin(0, 0)
	}
	if lastEvent != EventPickBroke {
		t.Fatalf("hp exhausted without pickBroke (got %q)", lastEvent)
	}
	if !e.Pick.Broken || e.Pick.HP != 0 {
		t.Fatalf("pick should be broken at 0 hp: %+v", e.Pick)
	}
	out, ev := e.PushPin(0, 50)
	if out.Result != ResultAlready || ev != "" {
		t.Fatalf("a broken pick must refuse pushes without drama: %+v %q", out, ev)
	}
}

func TestNewLockRecutsPickToCurrentStrength(t *testing.T) {
	e := New()
	for range RiggedPickStrength {
		e.PushPin(0, 0)
	}
	if !e.Pick.Broken {
		t.Fatal("setup: pick should be broken")
	}
	e.SetAct(2)
	e.SetParam("pickStrength", 9000)
	if !e.Pick.Broken {
		t.Fatal("a param write must NOT resurrect a snapped pick — only NewLock does")
	}
	serialBefore := e.Lock.Serial
	e.NewLock()
	if e.Pick.Broken || e.Pick.HP != 9000 || e.Pick.MaxHP != 9000 {
		t.Fatalf("newLock should cut a fresh pick at current strength: %+v", e.Pick)
	}
	if e.Lock.Serial <= serialBefore {
		t.Fatalf("newLock reused serial %d", e.Lock.Serial)
	}
}

func TestSetParamGatingClampsAndWake(t *testing.T) {
	e := New()
	// Act 1: refused silently, no wake.
	if ev := e.SetParam("pickStrength", 9000); ev != "" || e.Params.PickStrength != RiggedPickStrength || e.SentryAwake {
		t.Fatalf("act-1 write must be refused: ev=%q params=%+v awake=%v", ev, e.Params, e.SentryAwake)
	}
	e.SetAct(2)
	// First applied write wakes the sentry AND lands.
	if ev := e.SetParam("pickStrength", 999999); ev != EventSentryWake {
		t.Fatalf("first act-2 write should wake the sentry, got %q", ev)
	}
	if e.Params.PickStrength != MaxPickStrength {
		t.Fatalf("clamp failed: %d", e.Params.PickStrength)
	}
	if e.Pick.HP != MaxPickStrength || e.Pick.MaxHP != MaxPickStrength {
		t.Fatalf("an unbroken pick should heal to the new strength: %+v", e.Pick)
	}
	// Second write: no second wake.
	if ev := e.SetParam("forgiveness", -5); ev != "" {
		t.Fatalf("sentry should only wake once, got %q", ev)
	}
	if e.Params.Forgiveness != MinForgiveness {
		t.Fatalf("clamp failed: %d", e.Params.Forgiveness)
	}
	// Unknown knob: ignored, and must never wake the sentry on its own.
	e2 := New()
	e2.SetAct(2)
	if ev := e2.SetParam("vaultDoors", 0); ev != "" || e2.SentryAwake {
		t.Fatalf("unknown knob should be inert: ev=%q awake=%v", ev, e2.SentryAwake)
	}
}

// pickCleanly sets every pin at its exact target (no misses possible).
func pickCleanly(e *Engine) string {
	var ev string
	for i := range e.Lock.Pins {
		_, ev = e.PushPin(i, e.Lock.Pins[i].Target)
	}
	return ev
}

func TestMiracleWinSentryAsleep(t *testing.T) {
	e := New() // act 1, sentry asleep, hook nil — the miracle path must not consult it
	if ev := pickCleanly(e); ev != EventVaultOpen {
		t.Fatalf("clean pick with sentry asleep should open the vault, got %q", ev)
	}
	if !e.Won || !e.Lock.Picked {
		t.Fatalf("state after miracle: won=%v picked=%v", e.Won, e.Lock.Picked)
	}
}

// wake flips the engine into the act-2 "sentry hunting" configuration with a
// generous pick, without going through the JSON layer.
func wake(e *Engine) {
	e.SetAct(2)
	e.SetParam("pickStrength", 9000)
	e.SetParam("forgiveness", 40)
}

func TestHookSpawnDeniesWin(t *testing.T) {
	e := New()
	wake(e)
	e.Hook = func(user string) ([]Install, error) {
		return []Install{{Pins: DefaultPinCount}}, nil
	}
	if ev := pickCleanly(e); ev != EventSentrySpawned {
		t.Fatalf("unpicked install should spawn, got %q", ev)
	}
	if e.Won {
		t.Fatal("win must be denied while a live lock exists")
	}
	if e.Lock.Picked || e.Lock.Serial != 2 || e.LocksInstalled != 1 {
		t.Fatalf("spawned lock state wrong: %+v installed=%d", e.Lock, e.LocksInstalled)
	}
}

func TestHookNoInstallsOpensVault(t *testing.T) {
	e := New()
	wake(e)
	e.Hook = func(user string) ([]Install, error) { return nil, nil }
	if ev := pickCleanly(e); ev != EventVaultOpen {
		t.Fatalf("no installs should open the vault, got %q", ev)
	}
	if !e.Won {
		t.Fatal("won should be set")
	}
}

func TestPrePickedInstallsPopOpenWithoutRefiringHook(t *testing.T) {
	e := New()
	wake(e)
	calls := 0
	e.Hook = func(user string) ([]Install, error) {
		calls++
		return []Install{{Pins: DefaultPinCount, Picked: true}, {Pins: DefaultPinCount, Picked: true}}, nil
	}
	if ev := pickCleanly(e); ev != EventVaultOpen {
		t.Fatalf("pre-picked installs should pop open, got %q", ev)
	}
	if calls != 1 {
		t.Fatalf("hook fired %d times — pre-picked installs must not re-enter lockPicked", calls)
	}
	if e.LocksInstalled != 0 {
		t.Fatalf("pre-picked installs are not 'spawned locks': installed=%d", e.LocksInstalled)
	}
}

func TestZeroPinInstallCountsAsOpen(t *testing.T) {
	e := New()
	wake(e)
	e.Hook = func(user string) ([]Install, error) {
		return []Install{{Pins: 0}}, nil // a lock with no pins is not locked
	}
	if ev := pickCleanly(e); ev != EventVaultOpen {
		t.Fatalf("a pinless lock cannot bar the vault, got %q", ev)
	}
}

func TestOversizedInstallClampsToMaxPins(t *testing.T) {
	e := New()
	wake(e)
	e.Hook = func(user string) ([]Install, error) {
		return []Install{{Pins: 100}}, nil
	}
	if ev := pickCleanly(e); ev != EventSentrySpawned {
		t.Fatalf("want spawn, got %q", ev)
	}
	if len(e.Lock.Pins) != MaxPinCount {
		t.Fatalf("install should clamp to %d pins, got %d", MaxPinCount, len(e.Lock.Pins))
	}
}

func TestHookErrorInstallsOneDefaultLockAndDeniesWin(t *testing.T) {
	e := New()
	wake(e)
	e.Hook = func(user string) ([]Install, error) {
		return nil, &hookErr{"sentry7 crashed mid-pick: boom"}
	}
	if ev := pickCleanly(e); ev != EventSentrySpawned {
		t.Fatalf("crashed hook must behave as unpatched, got %q", ev)
	}
	if e.Won {
		t.Fatal("a crashing patch must never hand out wins")
	}
	if e.LastHookError == "" {
		t.Fatal("the error text must be carried for the editor visit")
	}
}

func TestNilHookDeniesWin(t *testing.T) {
	e := New()
	wake(e)
	// Hook never armed — a wiring bug. Must deny, not crash, not win.
	if ev := pickCleanly(e); ev != EventSentrySpawned {
		t.Fatalf("nil hook must deny the win with a spawn, got %q", ev)
	}
	if e.Won || e.LastHookError == "" {
		t.Fatalf("won=%v lastHookError=%q", e.Won, e.LastHookError)
	}
}

type hookErr struct{ s string }

func (h *hookErr) Error() string { return h.s }

// ============================================================================
// the rigging: honestly winnable, practically never
// ============================================================================

// sweepStrategy plays one lock the way a sharp human with no inside knowledge
// would: sweep the dial to find the "SO close" quiver (the almost flag), then
// walk inward one force unit at a time. This is close to optimal play against
// a hidden target — and it still loses act 1, which is the point of the rig.
func sweepStrategy(e *Engine) (won bool) {
	step := 2*(e.Params.Forgiveness+AlmostSlack) + 1 // widest sweep that cannot skip an almost-zone
	for pin := range e.Lock.Pins {
		guess := -1
		for f := TargetMin; f <= TargetMax; f += step {
			out, ev := e.PushPin(pin, f)
			if ev == EventVaultOpen {
				return true
			}
			if e.Pick.Broken {
				return false
			}
			if out.Result == ResultSet {
				guess = -2 // set by the sweep itself
				break
			}
			if out.Almost {
				guess = f
				break
			}
		}
		if guess == -2 {
			continue
		}
		if guess == -1 {
			return false // swept the whole dial on a broken pick's budget
		}
		// Walk inward from the quiver until it sets or the pick dies.
		for d := 1; d <= AlmostSlack+e.Params.Forgiveness; d++ {
			for _, f := range []int{guess + d, guess - d} {
				out, ev := e.PushPin(pin, f)
				if ev == EventVaultOpen {
					return true
				}
				if e.Pick.Broken {
					return false
				}
				if out.Result == ResultSet {
					d = 1000
					break
				}
			}
		}
		if !e.Lock.Pins[pin].Set {
			return false
		}
	}
	return e.Won
}

func TestRiggedTuningIsHonestlyWinnable(t *testing.T) {
	// The rig is never a fake fail: pushing every pin at its exact target
	// wins act 1 outright (this is the scripted "miracle" edge).
	e := New()
	if ev := pickCleanly(e); ev != EventVaultOpen || !e.Won {
		t.Fatalf("perfect play must win even rigged: ev=%q won=%v", ev, e.Won)
	}
}

func TestRiggedTuningIsPracticallyUnwinnable(t *testing.T) {
	const runs = 2000
	wins := 0
	for i := range runs {
		e := New()
		// Vary the lock so we sample many target layouts, not one.
		e.nextSerial = 1 + i
		e.NewLock()
		if sweepStrategy(e) {
			wins++
		}
	}
	if rate := float64(wins) / runs; rate > 0.01 {
		t.Fatalf("rigged win rate %.3f — act 1 is supposed to be a heartbreaker", rate)
	}
}

func TestBuffedTuningIsEasy(t *testing.T) {
	// After Zaq's admin advice ("make it say nine thousand") a blind push at
	// mid-dial sets EVERY pin: |50-target| <= 35 <= forgiveness 40 for all
	// targets in 25..85. Five pushes, zero misses, guaranteed.
	rng := rand.New(rand.NewSource(11))
	for range 500 {
		e := New()
		e.nextSerial = 1 + rng.Intn(100000)
		e.NewLock()
		wake(e)
		e.Hook = func(user string) ([]Install, error) { return nil, nil }
		var ev string
		for pin := range e.Lock.Pins {
			_, ev = e.PushPin(pin, 50)
		}
		if ev != EventVaultOpen || e.Pick.HP != e.Pick.MaxHP {
			t.Fatalf("serial %d: buffed play should be five clean sets (ev=%q hp=%d/%d)",
				e.Lock.Serial, ev, e.Pick.HP, e.Pick.MaxHP)
		}
	}
}
