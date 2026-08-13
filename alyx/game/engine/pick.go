package engine

// Pick is the tension tool. Slips and oversets chip it; at 0 hp it snaps and
// the act-1 drama begins. A snapped pick stays snapped until the next lock is
// cut (NewLock re-cuts the pick to the current PickStrength — that is how the
// admin buff becomes hit points).
type Pick struct {
	HP     int
	MaxHP  int
	Broken bool
}

// chip removes one hp and reports whether this chip was the snap. It is a
// no-op on an already-broken pick so a stray extra push can never re-fire the
// pickBroke drama.
func (p *Pick) chip() (snapped bool) {
	if p.Broken {
		return false
	}
	p.HP--
	if p.HP <= 0 {
		p.HP = 0
		p.Broken = true
		return true
	}
	return false
}
