package engine

import "math/rand"

// Lock geometry shared by every lock the vault ever cuts.
const (
	DefaultPinCount = 5  // pins per lock: enough drama, still one-thumb playable
	MaxPinCount     = 9  // clamp for sentry-installed locks: the lock card has finite width
	TargetMin       = 25 // force units: lowest secret sweet spot ...
	TargetMax       = 85 // ... and highest — keeps targets off the dial's edges so 0 and 100 are always honest misses
)

// Pin is one spring-loaded pin in the lock. Force is a 0..100 dial; the pin
// sets only if the release force lands inside
// [Target-Forgiveness, Target+Forgiveness].
type Pin struct {
	Target int // secret sweet spot, TargetMin..TargetMax, seeded per lock serial
	Set    bool
}

// Lock is the thing being picked.
type Lock struct {
	Serial int
	Pins   []Pin
	Picked bool // true when every pin is set (or the lock was installed pre-picked)
}

// cutLock deterministically cuts a lock: targets come from a rand seeded with
// the serial, so tests replay exactly and a watchdog-respawned engine re-cuts
// the identical lock from nothing but {serial, pin count} in the snapshot.
func cutLock(serial, pinCount int) Lock {
	rng := rand.New(rand.NewSource(int64(serial)))
	pins := make([]Pin, pinCount)
	for i := range pins {
		pins[i] = Pin{Target: TargetMin + rng.Intn(TargetMax-TargetMin+1)}
	}
	return Lock{Serial: serial, Pins: pins}
}

// allSet reports whether every pin is at the shear line.
func (l *Lock) allSet() bool {
	for _, p := range l.Pins {
		if !p.Set {
			return false
		}
	}
	return true
}
