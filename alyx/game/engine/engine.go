// Package engine is the lock/pin simulation behind alyx.html: pick durability
// with the rigged act-1 tuning, the admin-panel-editable Params, and the
// single lockPicked win gate that every act and every canonical solution
// resolves through. It knows nothing about JSON, yaegi, or the browser — the
// patch package wires those on top.
package engine

import (
	"encoding/json"
	"errors"
	"math/rand"
)

// ============================================================================
// tuning
// ============================================================================

const (
	// Factory (act-1) tuning. A ±1 window on a 0..100 dial, five pins, three
	// hit points: genuinely winnable in principle — the code path is honest and
	// the miracle win is scripted — but astronomically unlikely in practice.
	// Three misses (playtested at five, tuned back down): enough futile pushes
	// that the rigging reads as comedy, few enough that the snap lands fast.
	RiggedPickStrength = 3 // hp: three misses total, five pins to set
	RiggedForgiveness  = 1 // force units: sweet-spot half-width

	// Admin clamps. The panel writes land here; anything outside snaps in.
	MinPickStrength = 1    // hp: a 0-hp pick could never be swung even once
	MaxPickStrength = 9999 // hp: "make it say nine thousand. i want to see it." must fit
	MinForgiveness  = 0    // force units: exact-hit-only, for masochists
	MaxForgiveness  = 40   // force units: ±40 makes force 50 hit every 25..85 target — the buffed game is trivially easy on purpose

	// AlmostSlack is how far past the window still reads as "SO close" — wide
	// enough that act 1 taunts constantly, which is true, which is the joke.
	AlmostSlack = 7 // force units beyond forgiveness
)

// Params are the live tuning knobs. The admin panel edits THESE fields —
// there is no second copy; the act-1 rigging is just their factory values.
type Params struct {
	PickStrength int // pick hit points; a slip or overset chips 1  (rigged: 5)
	Forgiveness  int // sweet-spot half-width in force units        (rigged: 1)
}

// ============================================================================
// events + push outcomes (wire vocabulary — PINNED by the plan's §2 protocol)
// ============================================================================

// Events: each engine call reports at most one.
const (
	EventPickBroke       = "pickBroke"
	EventSentryWake      = "sentryWake"
	EventSentrySpawned   = "sentrySpawned"
	EventSentryStoodDown = "sentryStoodDown" // reserved by the protocol; vaultOpen carries the win instead
	EventVaultOpen       = "vaultOpen"
)

// Push results.
const (
	ResultSet     = "set"
	ResultSlip    = "slip"
	ResultOverset = "overset"
	ResultAlready = "already"
)

// PushOutcome is the judged result of one pushPin.
type PushOutcome struct {
	Result      string `json:"result"`
	PinsDropped []int  `json:"pinsDropped"`
	Almost      bool   `json:"almost"`
}

// ============================================================================
// the sentry hook boundary
// ============================================================================

// Install is a lock the sentry hook handed to the vault, translated from the
// two-field player-facing façade (game.Lock) at the patch boundary.
type Install struct {
	Pins   int
	Picked bool
}

// HookFunc is the LIVE interpreted sentry.OnLockPicked, wrapped by the patch
// package with install collection and panic recovery. The engine calls it at
// exactly one place: lockPicked.
type HookFunc func(user string) ([]Install, error)

// ============================================================================
// engine
// ============================================================================

// Engine is one playthrough's worth of state. All exported fields are
// serialized into the snapshot the main thread keeps for watchdog recovery.
type Engine struct {
	Act            int    // 1..3, set by the page at story beats; gates setParam
	Won            bool   // the vault is open
	SentryAwake    bool   // flipped by the first unauthorized param write
	User           string // who the sentry hook is told did the picking
	Params         Params
	Lock           Lock
	Pick           Pick
	LocksInstalled int    // total unpicked locks the sentry spawned — "lock #7" drama fuel
	Patched        bool   // a player edit is the live hook (set by the session, not here)
	LastHookError  string // most recent recovered hook crash, for the editor console

	// Hook is invoked with recover-protection by lockPicked. nil (a wiring
	// bug) is treated like a crashed hook: deny the win, install one default
	// lock — a bug must never hand out wins.
	Hook HookFunc

	nextSerial int
	rng        *rand.Rand // overset pin-drop choice; seeded so tests replay exactly
}

// New returns a factory-fresh engine: act 1, rigged params, lock #1 cut and
// waiting. The sentry hook is NOT armed here — the patch package does that.
func New() *Engine {
	e := &Engine{
		Act:        1,
		User:       "alyx",
		Params:     Params{PickStrength: RiggedPickStrength, Forgiveness: RiggedForgiveness},
		nextSerial: 1,
		rng:        rand.New(rand.NewSource(7)), // fixed seed: determinism, not fairness — which pin drops is flavor
	}
	e.NewLock()
	return e
}

// NewLock discards the current lock and cuts a fresh one — and re-cuts the
// PICK to the current PickStrength. The page relies on that second half to
// make the lock playable again after the act-1 break (Zaq "finds" another
// pick), and it is how the admin buff turns into hit points.
func (e *Engine) NewLock() {
	e.Lock = cutLock(e.nextSerial, DefaultPinCount)
	e.nextSerial++
	e.Pick = Pick{HP: e.Params.PickStrength, MaxHP: e.Params.PickStrength}
	e.Won = false
}

// SetUser records who the engine reports to the sentry hook.
func (e *Engine) SetUser(user string) {
	if user != "" {
		e.User = user
	}
}

// SetAct moves the story phase (clamped 1..3). The page owns WHEN acts
// advance; the engine only gates behavior (act 1 locks the params).
func (e *Engine) SetAct(act int) {
	e.Act = clamp(act, 1, 3)
}

// SetParam applies one admin-panel write. In act 1 the write is silently
// refused (the panel isn't open yet; a stale message must not wake the
// sentry). From act 2 on the value clamps in, and the FIRST successful write
// flips SentryAwake — the reply still carries the applied write, so she gets
// her strong pick AND the ominous banner on the same beat.
func (e *Engine) SetParam(name string, value int) (event string) {
	if e.Act < 2 {
		return ""
	}
	switch name {
	case "pickStrength":
		v := clamp(value, MinPickStrength, MaxPickStrength)
		e.Params.PickStrength = v
		e.Pick.MaxHP = v
		if !e.Pick.Broken {
			e.Pick.HP = v // the buff heals a pick that still exists; a snapped one needs NewLock
		}
	case "forgiveness":
		e.Params.Forgiveness = clamp(value, MinForgiveness, MaxForgiveness)
	default:
		return "" // unknown knob: ignore rather than wake the sentry over nothing
	}
	if !e.SentryAwake {
		e.SentryAwake = true
		return EventSentryWake
	}
	return ""
}

// PushPin judges one push: the player aimed at pin `pin` and released at
// `force` on the 0..100 dial. It returns the outcome plus at most one event
// (pickBroke, or whatever lockPicked emits on the final set).
func (e *Engine) PushPin(pin, force int) (PushOutcome, string) {
	out := PushOutcome{Result: ResultAlready, PinsDropped: []int{}}
	// Broken pick / picked lock / out-of-range pin: cost-free no-op. The page
	// prevents these; the engine refusing them keeps a stray double-tap from
	// double-firing drama.
	if e.Pick.Broken || e.Lock.Picked || e.Won || pin < 0 || pin >= len(e.Lock.Pins) {
		return out, ""
	}
	p := &e.Lock.Pins[pin]
	if p.Set {
		return out, ""
	}
	off := force - p.Target
	if off < 0 {
		off = -off
	}
	out.Almost = off <= e.Params.Forgiveness+AlmostSlack
	if off <= e.Params.Forgiveness {
		out.Result = ResultSet
		p.Set = true
		if e.Lock.allSet() {
			return out, e.lockPicked()
		}
		return out, ""
	}
	if force > p.Target+e.Params.Forgiveness {
		// The pin jams past the shear line and one random SET pin drops —
		// classic lockpicking cruelty; it is what makes act 1 "almost".
		out.Result = ResultOverset
		if d := e.dropRandomSetPin(); d >= 0 {
			out.PinsDropped = []int{d}
		}
	} else {
		out.Result = ResultSlip
	}
	if e.Pick.chip() {
		return out, EventPickBroke
	}
	return out, ""
}

// dropRandomSetPin drops one currently-set pin and returns its index, or -1
// if none were set.
func (e *Engine) dropRandomSetPin() int {
	set := make([]int, 0, len(e.Lock.Pins))
	for i, p := range e.Lock.Pins {
		if p.Set {
			set = append(set, i)
		}
	}
	if len(set) == 0 {
		return -1
	}
	d := set[e.rng.Intn(len(set))]
	e.Lock.Pins[d].Set = false
	return d
}

// lockPicked is the ONLY win gate — every act and every canonical solution
// resolves here, which is what makes the fiction honest.
func (e *Engine) lockPicked() string {
	e.Lock.Picked = true
	if !e.SentryAwake {
		e.Won = true
		return EventVaultOpen // act-1 miracle: she beat the rigged lock fair and square
	}
	installs, err := e.callHook()
	if err != nil {
		// A crashed (or missing) hook must not hand out wins OR softlock her:
		// behave exactly like the unpatched sentry — one fresh lock — and
		// carry the error text for her next editor visit.
		e.LastHookError = err.Error()
		installs = []Install{{Pins: DefaultPinCount}}
	} else {
		e.LastHookError = ""
	}
	var next *Lock
	for _, ins := range installs {
		pins := clamp(ins.Pins, 0, MaxPinCount) // the lock card has finite width
		if ins.Picked || pins == 0 {
			// Pre-picked installs pop open on arrival and are discarded (a
			// lock with no pins is not locked either). They must NOT re-enter
			// lockPicked: solution 2 would recurse — every open lock firing
			// the hook, installing more open locks — forever.
			continue
		}
		l := cutLock(e.nextSerial, pins)
		e.nextSerial++
		e.LocksInstalled++
		if next == nil {
			next = &l
		}
	}
	if next == nil {
		e.Won = true
		return EventVaultOpen // nothing survives between her and the vault
	}
	e.Lock = *next
	return EventSentrySpawned
}

// callHook fires the live sentry hook. A nil hook is reported as an error so
// lockPicked's deny-the-win path handles it — a wiring bug must never open
// the vault.
func (e *Engine) callHook() ([]Install, error) {
	if e.Hook == nil {
		return nil, errors.New("sentry hook not installed")
	}
	return e.Hook(e.User)
}

// ============================================================================
// snapshot / restore (watchdog recovery — the main thread owns the copy)
// ============================================================================

// snapshot is the wire form. Pin targets are NOT stored: they re-cut
// deterministically from {serial, pinCount}, which keeps the snapshot tiny
// and unspoofable-by-accident.
type snapshot struct {
	V              int    `json:"v"`
	Act            int    `json:"act"`
	Won            bool   `json:"won"`
	SentryAwake    bool   `json:"sentryAwake"`
	User           string `json:"user"`
	PickStrength   int    `json:"pickStrength"`
	Forgiveness    int    `json:"forgiveness"`
	Serial         int    `json:"serial"`
	PinCount       int    `json:"pinCount"`
	SetMask        []bool `json:"setMask"`
	Picked         bool   `json:"picked"`
	HP             int    `json:"hp"`
	MaxHP          int    `json:"maxHp"`
	Broken         bool   `json:"broken"`
	LocksInstalled int    `json:"locksInstalled"`
	NextSerial     int    `json:"nextSerial"`
	Patched        bool   `json:"patched"`
	LastHookError  string `json:"lastHookError,omitempty"`
}

// Snapshot marshals everything needed to rebuild this engine after a worker
// respawn. The live hook is NOT here — the main thread re-evals its last-good
// source through the patch package on restore.
func (e *Engine) Snapshot() string {
	mask := make([]bool, len(e.Lock.Pins))
	for i, p := range e.Lock.Pins {
		mask[i] = p.Set
	}
	b, err := json.Marshal(snapshot{
		V: 1, Act: e.Act, Won: e.Won, SentryAwake: e.SentryAwake, User: e.User,
		PickStrength: e.Params.PickStrength, Forgiveness: e.Params.Forgiveness,
		Serial: e.Lock.Serial, PinCount: len(e.Lock.Pins), SetMask: mask, Picked: e.Lock.Picked,
		HP: e.Pick.HP, MaxHP: e.Pick.MaxHP, Broken: e.Pick.Broken,
		LocksInstalled: e.LocksInstalled, NextSerial: e.nextSerial, Patched: e.Patched,
		LastHookError: e.LastHookError,
	})
	if err != nil {
		return "" // unreachable for this struct; "" reads as "no snapshot" upstream
	}
	return string(b)
}

// Restore rebuilds the engine from a Snapshot string. On any parse problem it
// returns an error and leaves the engine untouched, so the caller can fall
// back to factory state (a corrupt snapshot beats a dead page).
func (e *Engine) Restore(snap string) error {
	var s snapshot
	if err := json.Unmarshal([]byte(snap), &s); err != nil {
		return err
	}
	if s.PinCount < 1 || s.PinCount > MaxPinCount || s.Serial < 1 {
		return errors.New("snapshot: implausible lock geometry")
	}
	e.Act = clamp(s.Act, 1, 3)
	e.Won = s.Won
	e.SentryAwake = s.SentryAwake
	e.User = s.User
	if e.User == "" {
		e.User = "alyx"
	}
	e.Params = Params{
		PickStrength: clamp(s.PickStrength, MinPickStrength, MaxPickStrength),
		Forgiveness:  clamp(s.Forgiveness, MinForgiveness, MaxForgiveness),
	}
	e.Lock = cutLock(s.Serial, s.PinCount) // identical targets: cutLock is seeded by serial
	for i := range e.Lock.Pins {
		if i < len(s.SetMask) {
			e.Lock.Pins[i].Set = s.SetMask[i]
		}
	}
	e.Lock.Picked = s.Picked
	e.Pick = Pick{HP: s.HP, MaxHP: s.MaxHP, Broken: s.Broken}
	e.LocksInstalled = s.LocksInstalled
	// nextSerial must resume PAST every lock ever cut, or a respawned engine
	// would re-cut "lock #3" twice and the drama counter would lie.
	e.nextSerial = max(s.NextSerial, s.Serial+1)
	e.Patched = s.Patched
	e.LastHookError = s.LastHookError
	return nil
}

// clamp pins v into [lo, hi].
func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
