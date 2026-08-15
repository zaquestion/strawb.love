// Native tests of the FULL production path: the same Session JSON protocol
// the worker speaks, the same yaegi eval, the same interpreted hook fired by
// the same engine win gate. No wasm required — yaegi runs natively, so what
// passes here is what ships (the node smoke test re-proves it inside wasm).
package patch

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"testing"

	"strawb.love/game/engine"
	"strawb.love/game/sentry"
)

// ============================================================================
// harness
// ============================================================================

type rep struct {
	ID    int64 `json:"id"`
	Type  string
	State struct {
		Act         int
		Won         bool
		SentryAwake bool
		User        string
		Lock        struct {
			Serial int
			Pins   []struct{ Set bool }
			Picked bool
		}
		Pick struct {
			HP     int `json:"hp"`
			MaxHP  int `json:"maxHp"`
			Broken bool
		}
		Params struct {
			PickStrength int
			Forgiveness  int
		}
		LocksInstalled int
		Patched        bool
		LastHookError  string
		Snapshot       string
	}
	Event string
	Push  *struct {
		Result      string
		PinsDropped []int
		Almost      bool
	}
	Run *struct {
		Ok      bool
		Console []string
		Error   string
	}
	HookLog []string
	Error   string
}

var msgID int64

// call drives the session exactly like worker.js does: one JSON message in,
// one JSON reply out.
func call(t *testing.T, s *Session, action string, args map[string]any) rep {
	t.Helper()
	msgID++
	m := map[string]any{"id": msgID, "type": "act", "action": action}
	maps.Copy(m, args)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal %s: %v", action, err)
	}
	out := s.HandleSafe(string(raw))
	var r rep
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("unmarshal reply to %s: %v\nraw: %s", action, err, out)
	}
	if r.Type == "crash" {
		t.Fatalf("%s crashed: %s", action, r.Error)
	}
	if r.ID != msgID {
		t.Fatalf("%s: reply id %d, want %d", action, r.ID, msgID)
	}
	return r
}

// hackedSession boots and walks the story to the act-2 "sentry hunting"
// state: user set, params cranked, sentry awake.
func hackedSession(t *testing.T) *Session {
	t.Helper()
	s := NewSession()
	call(t, s, "boot", nil)
	call(t, s, "setUser", map[string]any{"user": "alyx"})
	call(t, s, "setAct", map[string]any{"act": 2})
	r := call(t, s, "setParam", map[string]any{"name": "pickStrength", "value": 9000})
	if r.Event != engine.EventSentryWake {
		t.Fatalf("first param write should wake the sentry, got %q", r.Event)
	}
	call(t, s, "setParam", map[string]any{"name": "forgiveness", "value": 40})
	return s
}

// pickLock pushes every unset pin at its exact target through the JSON path
// and returns the final reply (the one that fired lockPicked).
func pickLock(t *testing.T, s *Session) rep {
	t.Helper()
	var last rep
	// Walk over a private copy of the pins: the hook may swap the lock out
	// from under us on the last push. Already-set pins (e.g. after a restore)
	// are skipped — re-pushing them would be a cost-free "already".
	type probe struct{ pin, target int }
	var probes []probe
	for i, p := range s.Eng.Lock.Pins {
		if !p.Set {
			probes = append(probes, probe{i, p.Target})
		}
	}
	for _, pr := range probes {
		last = call(t, s, "pushPin", map[string]any{"pin": pr.pin, "force": pr.target})
		if last.Push == nil || last.Push.Result != engine.ResultSet {
			t.Fatalf("pin %d at its own target should set, got %+v", pr.pin, last.Push)
		}
	}
	return last
}

// edited returns DefaultSource with old replaced by new, failing loudly if
// the anchor text has drifted.
func edited(t *testing.T, old, new string) string {
	t.Helper()
	if !strings.Contains(sentry.DefaultSource, old) {
		t.Fatalf("edit anchor %q not in DefaultSource — §5 text drifted", old)
	}
	return strings.Replace(sentry.DefaultSource, old, new, 1)
}

// ============================================================================
// boot + the unpatched sentry
// ============================================================================

func TestBootState(t *testing.T) {
	s := NewSession()
	r := call(t, s, "boot", nil)
	st := r.State
	if st.Act != 1 || st.Won || st.SentryAwake || st.Patched {
		t.Fatalf("boot state wrong: %+v", st)
	}
	if st.Params.PickStrength != engine.RiggedPickStrength || st.Params.Forgiveness != engine.RiggedForgiveness {
		t.Fatalf("factory params must be the rigged ones: %+v", st.Params)
	}
	if len(st.Lock.Pins) != engine.DefaultPinCount || st.Lock.Serial != 1 || st.Lock.Picked {
		t.Fatalf("boot lock wrong: %+v", st.Lock)
	}
	if st.Pick.HP != engine.RiggedPickStrength || st.Pick.Broken {
		t.Fatalf("boot pick wrong: %+v", st.Pick)
	}
	if st.Snapshot == "" {
		t.Fatal("every reply must carry a snapshot")
	}
	if st.LastHookError != "" {
		t.Fatalf("default source failed at boot: %s", st.LastHookError)
	}
}

func TestUnpatchedSentrySpawnsAndCounts(t *testing.T) {
	s := hackedSession(t)
	r := pickLock(t, s)
	if r.Event != engine.EventSentrySpawned {
		t.Fatalf("factory sentry should spawn on pick, got %q", r.Event)
	}
	st := r.State
	if st.Won || st.Lock.Picked || st.Lock.Serial != 2 || st.LocksInstalled != 1 {
		t.Fatalf("spawn state wrong: %+v", st)
	}
	if len(r.HookLog) != 1 || !strings.Contains(r.HookLog[0], "INTRUDER: alyx") {
		t.Fatalf("pick-time hook output must ride the reply as hookLog: %v", r.HookLog)
	}
	// She tries again: same result, counter climbs — the punchline mechanism.
	r = pickLock(t, s)
	if r.Event != engine.EventSentrySpawned || r.State.LocksInstalled != 2 || r.State.Lock.Serial != 3 {
		t.Fatalf("second spawn wrong: event=%q %+v", r.Event, r.State)
	}
}

func TestAuthorizedUserZaqStandsDownUnpatched(t *testing.T) {
	// The factory whitelist already contains "zaq" — the engine reporting a
	// different user must sail through, proving the whitelist mechanism is
	// real before she ever edits it.
	s := hackedSession(t)
	call(t, s, "setUser", map[string]any{"user": "zaq"})
	r := pickLock(t, s)
	if r.Event != engine.EventVaultOpen || !r.State.Won {
		t.Fatalf("zaq is on the factory whitelist: event=%q won=%v", r.Event, r.State.Won)
	}
	if len(r.HookLog) != 1 || r.HookLog[0] != "hi zaq . carry on." {
		t.Fatalf("greeting wrong: %v", r.HookLog)
	}
}

// ============================================================================
// the three canonical solutions (PINNED §5) — each through runCode + pick
// ============================================================================

func TestSolutionStopMakingLocks(t *testing.T) {
	s := hackedSession(t)
	src := edited(t, "var NewLocksPerPick = 1", "var NewLocksPerPick = 0")
	r := call(t, s, "runCode", map[string]any{"source": src})
	if r.Run == nil || !r.Run.Ok || r.Run.Error != "" {
		t.Fatalf("patch should load: %+v", r.Run)
	}
	if !r.State.Patched {
		t.Fatal("patched flag should be set")
	}
	r = pickLock(t, s)
	if r.Event != engine.EventVaultOpen || !r.State.Won {
		t.Fatalf("solution 1 (locks=0) must open the vault: event=%q won=%v", r.Event, r.State.Won)
	}
	if len(r.HookLog) != 1 || !strings.Contains(r.HookLog[0], "installing 0 new lock(s)") {
		t.Fatalf("her code still ran and said so: %v", r.HookLog)
	}
}

func TestSolutionPrePickedLocks(t *testing.T) {
	s := hackedSession(t)
	src := edited(t,
		"lock.Picked = false // a lock that starts picked would be useless. obviously.",
		"lock.Picked = true")
	r := call(t, s, "runCode", map[string]any{"source": src})
	if r.Run == nil || !r.Run.Ok {
		t.Fatalf("patch should load: %+v", r.Run)
	}
	r = pickLock(t, s)
	if r.Event != engine.EventVaultOpen || !r.State.Won {
		t.Fatalf("solution 2 (pre-picked) must open the vault: event=%q won=%v", r.Event, r.State.Won)
	}
	if r.State.LocksInstalled != 0 {
		t.Fatalf("a lock that arrives open was never a lock: installed=%d", r.State.LocksInstalled)
	}
}

func TestSolutionWhitelistAlyx(t *testing.T) {
	s := hackedSession(t)
	src := edited(t, `var Authorized = []string{"zaq"}`, `var Authorized = []string{"zaq", "alyx"}`)
	r := call(t, s, "runCode", map[string]any{"source": src})
	if r.Run == nil || !r.Run.Ok {
		t.Fatalf("patch should load: %+v", r.Run)
	}
	r = pickLock(t, s)
	if r.Event != engine.EventVaultOpen || !r.State.Won {
		t.Fatalf("solution 3 (whitelist) must open the vault: event=%q won=%v", r.Event, r.State.Won)
	}
	if len(r.HookLog) != 1 || r.HookLog[0] != "hi alyx . carry on." {
		t.Fatalf("the whole point is this line: %v", r.HookLog)
	}
}

func TestSolutionsCombined(t *testing.T) {
	s := hackedSession(t)
	src := edited(t, `var Authorized = []string{"zaq"}`, `var Authorized = []string{"zaq", "alyx"}`)
	src = strings.Replace(src, "var NewLocksPerPick = 1", "var NewLocksPerPick = 0", 1)
	src = strings.Replace(src, "lock.Picked = false // a lock that starts picked would be useless. obviously.", "lock.Picked = true", 1)
	r := call(t, s, "runCode", map[string]any{"source": src})
	if r.Run == nil || !r.Run.Ok {
		t.Fatalf("combined patch should load: %+v", r.Run)
	}
	if r = pickLock(t, s); r.Event != engine.EventVaultOpen {
		t.Fatalf("belt AND suspenders should still open the vault: %q", r.Event)
	}
}

// ============================================================================
// rejected + hostile patches
// ============================================================================

func TestSyntaxErrorSurfacesAndKeepsOldHook(t *testing.T) {
	s := hackedSession(t)
	r := call(t, s, "runCode", map[string]any{"source": "package sentry\n\nfunc OnLockPicked(user string) {"})
	if r.Run == nil || r.Run.Ok || r.Run.Error == "" {
		t.Fatalf("syntax error should be reported: %+v", r.Run)
	}
	if r.State.Patched {
		t.Fatal("a failed run must not mark the engine patched")
	}
	// The previous (default) hook must still be live: pick → spawn.
	if r = pickLock(t, s); r.Event != engine.EventSentrySpawned {
		t.Fatalf("default hook should survive a failed run: %q", r.Event)
	}
}

func TestMissingHookRejected(t *testing.T) {
	s := hackedSession(t)
	r := call(t, s, "runCode", map[string]any{"source": "package sentry\n\nvar NewLocksPerPick = 0"})
	if r.Run == nil || r.Run.Ok || r.Run.Error != MissingHookMsg {
		t.Fatalf("want the pinned missing-hook line, got %+v", r.Run)
	}
}

func TestWrongHookShapeRejected(t *testing.T) {
	s := hackedSession(t)
	r := call(t, s, "runCode", map[string]any{"source": "package sentry\n\nfunc OnLockPicked() {}"})
	if r.Run == nil || r.Run.Ok || r.Run.Error != BadHookShapeMsg {
		t.Fatalf("want the pinned shape line, got %+v", r.Run)
	}
}

func TestUndefinedNameErrorIsReadable(t *testing.T) {
	s := hackedSession(t)
	r := call(t, s, "runCode", map[string]any{"source": "package sentry\n\nimport \"game\"\n\nfunc OnLockPicked(user string) {\n\tgame.Instal(game.NewLock())\n}"})
	if r.Run == nil || r.Run.Ok {
		t.Fatalf("typo should fail: %+v", r.Run)
	}
	if !strings.Contains(r.Run.Error, "Instal") {
		t.Fatalf("the error should name her typo: %q", r.Run.Error)
	}
}

func TestLogOnlyEditStillSpawns(t *testing.T) {
	s := hackedSession(t)
	src := edited(t, `game.Log("INTRUDER:", user, "— installing", NewLocksPerPick, "new lock(s)")`,
		`game.Log("INTRUDER:", user, "— installing", NewLocksPerPick, "new lock(s)")`+"\n\tgame.Log(\"pretty please?\")")
	r := call(t, s, "runCode", map[string]any{"source": src})
	if r.Run == nil || !r.Run.Ok {
		t.Fatalf("log-only edit should load: %+v", r.Run)
	}
	r = pickLock(t, s)
	if r.Event != engine.EventSentrySpawned || r.State.Won {
		t.Fatalf("asking nicely is not a patch: event=%q won=%v", r.Event, r.State.Won)
	}
	if len(r.HookLog) != 2 || r.HookLog[1] != "pretty please?" {
		t.Fatalf("her extra log should still show: %v", r.HookLog)
	}
}

func TestHookRuntimeCrashDeniesWinAndCarriesError(t *testing.T) {
	s := hackedSession(t)
	src := "package sentry\n\nvar boom []int\n\nfunc OnLockPicked(user string) {\n\t_ = boom[3]\n}"
	r := call(t, s, "runCode", map[string]any{"source": src})
	if r.Run == nil || !r.Run.Ok {
		t.Fatalf("the crash is at pick time, load should succeed: %+v", r.Run)
	}
	r = pickLock(t, s)
	if r.Event != engine.EventSentrySpawned || r.State.Won {
		t.Fatalf("a crashing hook must behave as unpatched: event=%q won=%v", r.Event, r.State.Won)
	}
	if !strings.Contains(r.State.LastHookError, "crashed mid-pick") {
		t.Fatalf("the crash must be carried for the editor: %q", r.State.LastHookError)
	}
	// The NEXT healthy patch clears the stigma.
	src = edited(t, `var Authorized = []string{"zaq"}`, `var Authorized = []string{"zaq", "alyx"}`)
	call(t, s, "runCode", map[string]any{"source": src})
	if r = pickLock(t, s); r.Event != engine.EventVaultOpen || r.State.LastHookError != "" {
		t.Fatalf("recovery after a crashed patch: event=%q err=%q", r.Event, r.State.LastHookError)
	}
}

func TestEvalTimeInstallSmugglingDoesNotCount(t *testing.T) {
	s := hackedSession(t)
	// Locks installed at LOAD time (init trickery) must not be waiting in the
	// queue at pick time — only what the hook installs during the pick counts.
	src := "package sentry\n\nimport \"game\"\n\nfunc init() {\n\tgame.Install(game.NewLock())\n}\n\nfunc OnLockPicked(user string) {}"
	r := call(t, s, "runCode", map[string]any{"source": src})
	if r.Run == nil || !r.Run.Ok {
		t.Fatalf("load should succeed: %+v", r.Run)
	}
	r = pickLock(t, s)
	if r.Event != engine.EventVaultOpen || !r.State.Won {
		t.Fatalf("a do-nothing hook opens the vault regardless of init smuggling: event=%q won=%v", r.Event, r.State.Won)
	}
}

func TestEvalTimeLogsLandInRunConsole(t *testing.T) {
	s := hackedSession(t)
	src := "package sentry\n\nimport \"game\"\n\nfunc init() {\n\tgame.Log(\"hello from load time\")\n}\n\nfunc OnLockPicked(user string) {}"
	r := call(t, s, "runCode", map[string]any{"source": src})
	if r.Run == nil || !r.Run.Ok {
		t.Fatalf("load should succeed: %+v", r.Run)
	}
	if len(r.Run.Console) != 1 || r.Run.Console[0] != "hello from load time" {
		t.Fatalf("init-time logs belong in run.console: %+v", r.Run.Console)
	}
	if len(r.HookLog) != 0 {
		t.Fatalf("nothing should double-report as hookLog: %v", r.HookLog)
	}
}

// ============================================================================
// restore (the watchdog respawn path)
// ============================================================================

func TestRestoreRoundTripWithPatch(t *testing.T) {
	s := hackedSession(t)
	src := edited(t, `var Authorized = []string{"zaq"}`, `var Authorized = []string{"zaq", "alyx"}`)
	call(t, s, "runCode", map[string]any{"source": src})
	// Set two pins so the restored lock has texture.
	targets := []int{s.Eng.Lock.Pins[0].Target, s.Eng.Lock.Pins[1].Target}
	call(t, s, "pushPin", map[string]any{"pin": 0, "force": targets[0]})
	r := call(t, s, "pushPin", map[string]any{"pin": 1, "force": targets[1]})
	snap := r.State.Snapshot

	// Fresh worker after a watchdog kill: boot, then restore snapshot+source.
	s2 := NewSession()
	call(t, s2, "boot", nil)
	r2 := call(t, s2, "restore", map[string]any{"snapshot": snap, "source": src})
	if r2.State.Snapshot != snap {
		t.Fatalf("restore drifted:\n before %s\n after  %s", snap, r2.State.Snapshot)
	}
	if !r2.State.Patched {
		t.Fatal("patched flag must survive the respawn")
	}
	// And the re-eval'd patch must be LIVE, not just remembered.
	r2 = pickLock(t, s2)
	if r2.Event != engine.EventVaultOpen || len(r2.HookLog) != 1 || r2.HookLog[0] != "hi alyx . carry on." {
		t.Fatalf("restored patch should still greet her: event=%q log=%v", r2.Event, r2.HookLog)
	}
}

func TestRestoreCorruptSnapshotFallsBackToFactory(t *testing.T) {
	s := NewSession()
	r := call(t, s, "restore", map[string]any{"snapshot": "{definitely not json", "source": ""})
	if r.State.Act != 1 || r.State.Won || r.State.Lock.Serial != 1 {
		t.Fatalf("corrupt snapshot should leave factory state: %+v", r.State)
	}
}

// ============================================================================
// protocol hygiene
// ============================================================================

func TestUnknownActionRepliesWithState(t *testing.T) {
	s := NewSession()
	r := call(t, s, "defragTheMainframe", nil)
	if r.State.Snapshot == "" {
		t.Fatal("unknown actions should degrade to a state reply, not crash")
	}
}

func TestBadJSONCrashReply(t *testing.T) {
	s := NewSession()
	out := s.HandleSafe("{not json")
	var r rep
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("crash reply must still be JSON: %v (%s)", err, out)
	}
	if r.Type != "crash" || r.Error == "" {
		t.Fatalf("want a crash reply, got %s", out)
	}
}

func TestFloatForcesTruncate(t *testing.T) {
	s := hackedSession(t)
	target := s.Eng.Lock.Pins[0].Target
	// The page's force dial animates in floats; target+0.9 must judge as target.
	r := call(t, s, "pushPin", map[string]any{"pin": 0, "force": float64(target) + 0.9})
	if r.Push == nil || r.Push.Result != engine.ResultSet {
		t.Fatalf("float force should truncate to a set: %+v", r.Push)
	}
}

func TestRunConsoleNeverNull(t *testing.T) {
	s := NewSession()
	msgID++
	raw := fmt.Sprintf(`{"id":%d,"type":"act","action":"runCode","source":%q}`, msgID, sentry.DefaultSource)
	out := s.HandleSafe(raw)
	if strings.Contains(out, `"console":null`) {
		t.Fatalf("run.console must marshal as [], never null: %s", out)
	}
}
