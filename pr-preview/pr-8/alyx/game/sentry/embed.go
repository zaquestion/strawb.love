// Package sentry holds SENTRY-7's real source. sentry7.go is simultaneously:
//
//   - a real compiling Go file (natively tested in sentry_test.go),
//   - the embedded DefaultSource yaegi evaluates at boot, and
//   - the exact text the in-game editor shows the player.
//
// One file, three duties — so the code she reads IS the code that runs.
package sentry

import (
	_ "embed"
	"strings"
)

//go:embed sentry7.go
var rawSource string

// DefaultSource is sentry7.go exactly as the editor shows it. The trailing
// newline gofmt requires on the disk file is trimmed because the page carries
// a byte-identical JS copy (join("\n"), no final newline) and the two must
// never drift — sentry_test.go pins both facts.
var DefaultSource = strings.TrimSuffix(rawSource, "\n")
