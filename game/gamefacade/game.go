// Package game is the tiny API surface SENTRY-7's source code is allowed to
// touch — four names, nothing else. Keeping the surface this small is what
// keeps the player-facing code honest AND the wasm build slim (no
// stdlib.Symbols, no fmt).
//
// The package exists in two bindings on purpose:
//
//   - natively, THIS package is what makes sentry7.go a real compiling Go file
//     (the module's `replace game => ./gamefacade` resolves the bare import),
//     so the text the player reads is gofmt-able and unit-testable;
//   - in the interpreter, yaegi resolves `import "game"` to the patch
//     package's interp.Exports (key "game/game") — the same four names, bound
//     as closures over the live engine.
package game

// Lock is the two-field lock façade SENTRY-7 hands to the vault. The engine
// translates it into a real lock (serial, seeded pin targets) on consume;
// keeping the player-facing type two fields big is the point.
type Lock struct {
	Pins   int
	Picked bool
}

// The native implementations are swappable vars so sentry_test.go can observe
// the hook's behavior. They default to harmless stand-ins (never nil) because
// the wasm build compiles this package too, and a nil func would turn a
// wiring slip into a crash instead of a visible no-op.
var (
	// LogFn receives one console line's worth of operands.
	LogFn = func(args ...interface{}) {}
	// NewLockFn cuts a fresh façade lock.
	NewLockFn = func() *Lock { return &Lock{Pins: 5} }
	// InstallFn hands a lock to the vault.
	InstallFn = func(l *Lock) {}
)

// Log prints one line to the vault's debug console.
func Log(args ...interface{}) { LogFn(args...) }

// NewLock returns a fresh lock: 5 pins, not picked.
func NewLock() *Lock { return NewLockFn() }

// Install hands a lock to the vault (queued; the engine consumes the queue
// when the current lock pops open).
func Install(l *Lock) { InstallFn(l) }
