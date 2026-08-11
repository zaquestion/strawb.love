// Package patch wires yaegi to the engine: it exports the curated "game"
// package into the interpreter, evaluates SENTRY-7 source (the embedded
// default at boot, the player's edit on RUN), extracts OnLockPicked as the
// live hook, and speaks the engineCall JSON protocol — the same bytes whether
// called natively (tests) or from the worker (wasm).
package patch

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/traefik/yaegi/interp"

	"game"
	"strawb.love/game/engine"
	"strawb.love/game/sentry"
)

// ============================================================================
// player-facing rejection lines (PINNED — the page mock carries copies)
// ============================================================================

const (
	// MissingHookMsg fires when the eval'd source has no sentry.OnLockPicked.
	MissingHookMsg = "SENTRY-7 patch rejected: OnLockPicked is missing — it needs that to boot."
	// BadHookShapeMsg fires when it exists but is not func(user string).
	BadHookShapeMsg = "SENTRY-7 patch rejected: OnLockPicked must be func(user string) — it is called with just the user."
)

// hookShape is the one signature the engine knows how to call.
var hookShape = reflect.TypeOf(func(string) {})

// ============================================================================
// Patcher — interpreter lifecycle + the curated exports
// ============================================================================

// Patcher owns the live interpreted hook and the game.Log/Install plumbing.
// Each Eval builds a FRESH interpreter (stale globals from an old patch must
// not leak into a new one); the closures below are bound to the Patcher, so
// they stay valid across interpreters.
type Patcher struct {
	eng      *engine.Engine
	logs     []string      // game.Log lines since last TakeLogs (eval-time and pick-time)
	installs []*game.Lock  // locks queued by game.Install during the current hook call
	hookVal  reflect.Value // the LIVE interpreted sentry.OnLockPicked
}

// New binds a Patcher to an engine and arms the engine's hook boundary.
func New(eng *engine.Engine) *Patcher {
	p := &Patcher{eng: eng}
	eng.Hook = p.runHook
	return p
}

// exports is the ENTIRE surface player code can touch: four names. No fmt,
// no os, no stdlib.Symbols — that keeps the wasm slim (~2.6MB gz) and the
// player-facing code honest about what it can do.
func (p *Patcher) exports() interp.Exports {
	return interp.Exports{
		"game/game": map[string]reflect.Value{
			"Log":     reflect.ValueOf(p.gameLog),
			"NewLock": reflect.ValueOf(p.gameNewLock),
			"Install": reflect.ValueOf(p.gameInstall),
			"Lock":    reflect.ValueOf((*game.Lock)(nil)),
		},
	}
}

// gameLog implements game.Log: one console line per call, fmt.Sprintln
// spacing (that is where "hi alyx . carry on." gets its charming gap).
func (p *Patcher) gameLog(args ...interface{}) {
	p.logs = append(p.logs, strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

// gameNewLock implements game.NewLock: a fresh five-pin façade lock.
func (p *Patcher) gameNewLock() *game.Lock {
	return &game.Lock{Pins: engine.DefaultPinCount}
}

// gameInstall implements game.Install: queue the lock for the engine to
// consume at the end of the current lockPicked.
func (p *Patcher) gameInstall(l *game.Lock) {
	if l != nil {
		p.installs = append(p.installs, l)
	}
}

// TakeLogs drains accumulated game.Log lines. The session routes them to
// run.console (RUN button) or hookLog (pick-time) depending on the action.
func (p *Patcher) TakeLogs() []string {
	l := p.logs
	p.logs = nil
	return l
}

// Eval type-checks and runs source in a fresh interpreter, then swaps the
// interpreted OnLockPicked in as the live hook. On ANY failure the previous
// hook stays live — a bad RUN never bricks a working patch. Console output
// (game.Log at package init time) is returned even when the eval fails, so
// she sees what ran before it fell over.
func (p *Patcher) Eval(source string) (console []string, err error) {
	defer func() {
		console = p.TakeLogs()
		if r := recover(); r != nil {
			// yaegi panics on some malformed inputs instead of returning an
			// error; the engine must outlive anything she can type.
			err = fmt.Errorf("SENTRY-7 patch crashed while loading: %v", r)
		}
	}()
	i := interp.New(interp.Options{})
	if uerr := i.Use(p.exports()); uerr != nil {
		return nil, fmt.Errorf("SENTRY-7 internal fault: %v", uerr)
	}
	if _, everr := i.Eval(source); everr != nil {
		return nil, prettyErr(everr)
	}
	v, herr := i.Eval("sentry.OnLockPicked")
	if herr != nil || !v.IsValid() {
		return nil, errors.New(MissingHookMsg)
	}
	if v.Kind() != reflect.Func || v.Type() != hookShape {
		return nil, errors.New(BadHookShapeMsg)
	}
	p.hookVal = v
	return nil, nil
}

// runHook is the engine's HookFunc: fire the LIVE interpreted OnLockPicked
// and hand back whatever locks her code installed during the call.
func (p *Patcher) runHook(user string) (installs []engine.Install, err error) {
	// Only locks installed by THIS invocation count — a stale queue from an
	// eval-time game.Install (top-level trickery) must not smuggle locks in.
	p.installs = nil
	defer func() {
		if r := recover(); r != nil {
			// Interpreted runtime faults (nil deref, out-of-range, her own
			// panic calls) surface as Go panics on Call. Recover keeps the
			// engine alive; lockPicked treats the error as "unpatched".
			err = fmt.Errorf("sentry7 crashed mid-pick: %v", r)
		}
	}()
	if !p.hookVal.IsValid() {
		return nil, errors.New("no sentry patch loaded")
	}
	p.hookVal.Call([]reflect.Value{reflect.ValueOf(user)})
	for _, l := range p.installs {
		installs = append(installs, engine.Install{Pins: l.Pins, Picked: l.Picked})
	}
	return installs, nil
}

// prettyErr trims yaegi's error text down to the line she can act on. Yaegi
// messages are already position-prefixed ("3:14: undefined: ..."); we keep
// them verbatim but collapse the occasional multi-line dump to its first
// line so the console stays readable on a phone.
func prettyErr(err error) error {
	msg := strings.TrimSpace(err.Error())
	if i := strings.IndexByte(msg, '\n'); i > 0 {
		msg = msg[:i]
	}
	return errors.New(msg)
}

// ============================================================================
// Session — the engineCall JSON API (protocol PINNED by the plan's §2)
// ============================================================================

// Session owns one engine + one patcher and turns protocol messages into
// engine calls. wasmmain feeds it from globalThis.engineCall; patch_test
// feeds it the same JSON natively.
type Session struct {
	Eng *engine.Engine
	P   *Patcher
}

// NewSession boots a factory-fresh session: new engine, default sentry
// source evaluated through the SAME yaegi path player edits use — the source
// she reads is the code that runs, from minute one.
func NewSession() *Session {
	s := &Session{}
	s.boot()
	return s
}

// msg is the parsed act message. Numeric fields arrive as float64 because the
// page's force dial animates in floats; they truncate at the engine boundary.
type msg struct {
	ID       int64   `json:"id"`
	Type     string  `json:"type"`
	Action   string  `json:"action"`
	Pin      int     `json:"pin"`
	Force    float64 `json:"force"`
	Name     string  `json:"name"`
	Value    float64 `json:"value"`
	User     string  `json:"user"`
	Source   string  `json:"source"`
	Snapshot string  `json:"snapshot"`
	Act      int     `json:"act"`
}

// Wire shapes for the reply (field names PINNED).
type runOut struct {
	Ok      bool     `json:"ok"`
	Console []string `json:"console"`
	Error   string   `json:"error"`
}

type pinOut struct {
	Set bool `json:"set"`
}

type stateOut struct {
	Act            int      `json:"act"`
	Won            bool     `json:"won"`
	SentryAwake    bool     `json:"sentryAwake"`
	User           string   `json:"user"`
	Lock           lockOut  `json:"lock"`
	Pick           pickOut  `json:"pick"`
	Params         paramOut `json:"params"`
	LocksInstalled int      `json:"locksInstalled"`
	Patched        bool     `json:"patched"`
	LastHookError  string   `json:"lastHookError,omitempty"`
	Snapshot       string   `json:"snapshot"`
}

type lockOut struct {
	Serial int      `json:"serial"`
	Pins   []pinOut `json:"pins"`
	Picked bool     `json:"picked"`
}

type pickOut struct {
	HP     int  `json:"hp"`
	MaxHP  int  `json:"maxHp"`
	Broken bool `json:"broken"`
}

type paramOut struct {
	PickStrength int `json:"pickStrength"`
	Forgiveness  int `json:"forgiveness"`
}

type reply struct {
	ID      int64               `json:"id"`
	Type    string              `json:"type"`
	State   *stateOut           `json:"state"`
	Event   string              `json:"event"`
	Push    *engine.PushOutcome `json:"push,omitempty"`
	Run     *runOut             `json:"run,omitempty"`
	HookLog []string            `json:"hookLog,omitempty"`
}

// HandleSafe is Handle behind a recover: whatever happens, the caller gets a
// JSON string back. A panic becomes a {type:"crash"} reply, which the main
// thread treats exactly like a watchdog timeout (terminate → respawn →
// restore) — so even an engine bug degrades to one lost turn.
func (s *Session) HandleSafe(raw string) (out string) {
	defer func() {
		if r := recover(); r != nil {
			b, _ := json.Marshal(map[string]string{"type": "crash", "error": fmt.Sprint(r)})
			out = string(b)
		}
	}()
	return s.Handle(raw)
}

// Handle executes one protocol message and returns the reply JSON.
func (s *Session) Handle(raw string) string {
	var m msg
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		b, _ := json.Marshal(map[string]string{"type": "crash", "error": "bad message: " + err.Error()})
		return string(b)
	}
	r := reply{ID: m.ID, Type: "result", Event: ""}
	switch m.Action {
	case "boot":
		s.boot()
	case "restore":
		s.restore(m.Snapshot, m.Source)
	case "newLock":
		s.Eng.NewLock()
	case "pushPin":
		out, ev := s.Eng.PushPin(m.Pin, int(m.Force))
		r.Push = &out
		r.Event = ev
	case "setParam":
		r.Event = s.Eng.SetParam(m.Name, int(m.Value))
	case "setUser":
		s.Eng.SetUser(m.User)
	case "setAct":
		s.Eng.SetAct(m.Act)
	case "runCode":
		r.Run = s.runCode(m.Source)
	default:
		// Unknown action: reply with current state rather than crash — a
		// newer page against an older cached wasm should degrade, not die.
	}
	// game.Log output from a hook fired during THIS call (pushPin, mostly)
	// rides the reply as hookLog so the console shows pick-time chatter.
	// runCode is the exception: its Eval already drained eval-time logs into
	// run.console, so this is empty there by construction.
	if logs := s.P.TakeLogs(); len(logs) > 0 {
		r.HookLog = logs
	}
	r.State = s.stateOut()
	b, err := json.Marshal(r)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"type": "crash", "error": "reply marshal: " + err.Error()})
	}
	return string(b)
}

// boot builds a factory-fresh engine and arms the default sentry source.
func (s *Session) boot() {
	s.Eng = engine.New()
	s.P = New(s.Eng)
	if _, err := s.P.Eval(sentry.DefaultSource); err != nil {
		// The embedded source failing to eval is a build defect (pinned by
		// tests). Keep the engine alive and surface the fault where the
		// editor will show it instead of dying at boot.
		s.Eng.LastHookError = "sentry7 default source failed: " + err.Error()
	}
	s.Eng.Patched = false
}

// restore rebuilds from the main thread's snapshot after a worker respawn,
// then re-evals the last-good source ("" means the default). Corrupt
// snapshot: stay factory — a fresh vault beats a dead page.
func (s *Session) restore(snap, source string) {
	s.boot()
	if snap == "" {
		return
	}
	if err := s.Eng.Restore(snap); err != nil {
		return
	}
	if source != "" {
		if _, err := s.P.Eval(source); err != nil {
			// The last-good source failing on re-eval should be impossible
			// (it eval'd once already); if it happens the default hook from
			// boot stays live and the snapshot's Patched flag is corrected.
			s.Eng.Patched = false
			s.Eng.LastHookError = "patch lost in recovery: " + err.Error()
		}
	}
}

// runCode evaluates a player edit. Success swaps the live hook and marks the
// engine patched; failure leaves the previous hook live.
func (s *Session) runCode(source string) *runOut {
	console, err := s.P.Eval(source)
	if console == nil {
		console = []string{} // "[]" on the wire, never null — the page iterates it blindly
	}
	out := &runOut{Ok: err == nil, Console: console}
	if err != nil {
		out.Error = err.Error()
	} else {
		s.Eng.Patched = true
	}
	return out
}

// stateOut projects the engine into the pinned reply schema. Pin targets are
// deliberately absent: the page must never be able to peek at the sweet spot.
func (s *Session) stateOut() *stateOut {
	e := s.Eng
	pins := make([]pinOut, len(e.Lock.Pins))
	for i, p := range e.Lock.Pins {
		pins[i] = pinOut{Set: p.Set}
	}
	return &stateOut{
		Act: e.Act, Won: e.Won, SentryAwake: e.SentryAwake, User: e.User,
		Lock:           lockOut{Serial: e.Lock.Serial, Pins: pins, Picked: e.Lock.Picked},
		Pick:           pickOut{HP: e.Pick.HP, MaxHP: e.Pick.MaxHP, Broken: e.Pick.Broken},
		Params:         paramOut{PickStrength: e.Params.PickStrength, Forgiveness: e.Params.Forgiveness},
		LocksInstalled: e.LocksInstalled,
		Patched:        e.Patched,
		LastHookError:  e.LastHookError,
		Snapshot:       e.Snapshot(),
	}
}
