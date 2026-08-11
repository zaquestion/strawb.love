package sentry

import (
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"game"
)

// capture swaps the native game façade for recorders and returns them plus a
// restore func. sentry7.go compiles natively against the same façade yaegi
// exports in the browser, so these tests exercise the hook's REAL body.
func capture() (logs *[]string, installs *[]*game.Lock, restore func()) {
	var l []string
	var ins []*game.Lock
	oldLog, oldNew, oldInstall := game.LogFn, game.NewLockFn, game.InstallFn
	game.LogFn = func(args ...any) {
		l = append(l, strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
	}
	game.InstallFn = func(lk *game.Lock) { ins = append(ins, lk) }
	return &l, &ins, func() {
		game.LogFn, game.NewLockFn, game.InstallFn = oldLog, oldNew, oldInstall
	}
}

func TestOnLockPickedAuthorizedUserStandsDown(t *testing.T) {
	logs, installs, restore := capture()
	defer restore()
	OnLockPicked("zaq")
	if len(*installs) != 0 {
		t.Fatalf("authorized user should install nothing, got %d locks", len(*installs))
	}
	if len(*logs) != 1 || (*logs)[0] != "hi zaq . carry on." {
		t.Fatalf("greeting wrong: %q", *logs)
	}
}

func TestOnLockPickedIntruderInstallsOneUnpickedLock(t *testing.T) {
	logs, installs, restore := capture()
	defer restore()
	OnLockPicked("alyx")
	if len(*installs) != 1 {
		t.Fatalf("factory sentry installs exactly one lock, got %d", len(*installs))
	}
	if (*installs)[0].Picked {
		t.Fatal("the factory lock must arrive unpicked (that comment in the source is a HINT, not the behavior)")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "INTRUDER: alyx") {
		t.Fatalf("intruder line wrong: %q", *logs)
	}
}

func TestOnLockPickedHonorsNewLocksPerPick(t *testing.T) {
	_, installs, restore := capture()
	defer restore()
	old := NewLocksPerPick
	defer func() { NewLocksPerPick = old }()
	NewLocksPerPick = 0
	OnLockPicked("alyx")
	if len(*installs) != 0 {
		t.Fatalf("NewLocksPerPick=0 must install nothing (canonical solution 1), got %d", len(*installs))
	}
	NewLocksPerPick = 3
	OnLockPicked("alyx")
	if len(*installs) != 3 {
		t.Fatalf("NewLocksPerPick=3 must install 3, got %d", len(*installs))
	}
}

func TestOnLockPickedHonorsWhitelist(t *testing.T) {
	logs, installs, restore := capture()
	defer restore()
	old := Authorized
	defer func() { Authorized = old }()
	Authorized = []string{"zaq", "alyx"}
	OnLockPicked("alyx")
	if len(*installs) != 0 {
		t.Fatal("whitelisted user must not spawn locks (canonical solution 3)")
	}
	if len(*logs) != 1 || (*logs)[0] != "hi alyx . carry on." {
		t.Fatalf("the whole point is this line: %q", *logs)
	}
}

// TestDefaultSourceMatchesDiskFile pins the embed: what yaegi evals IS the
// file in this package, byte for byte (modulo the final newline gofmt puts on
// disk files, which the editor copy does not carry).
func TestDefaultSourceMatchesDiskFile(t *testing.T) {
	disk, err := os.ReadFile("sentry7.go")
	if err != nil {
		t.Fatalf("read sentry7.go: %v", err)
	}
	if DefaultSource+"\n" != string(disk) {
		t.Fatal("DefaultSource drifted from sentry7.go on disk")
	}
	if strings.HasSuffix(DefaultSource, "\n") {
		t.Fatal("DefaultSource must not end in a newline — the page's JS copy does not")
	}
}

// TestDefaultSourceMatchesPageCopy pins DefaultSource to the DEFAULT_SOURCE
// array the REAL ../../alyx.html ships (plan §5) — read from the bundled page
// itself, not a third hand-maintained copy, so drift on EITHER side fails this
// test. If it fails, alyx.html and the engine are showing her two different
// files — fix the drift, do not weaken the test.
func TestDefaultSourceMatchesPageCopy(t *testing.T) {
	page, err := os.ReadFile("../../alyx.html")
	if err != nil {
		t.Fatalf("read ../../alyx.html (run from game/sentry): %v", err)
	}
	got, err := pageDefaultSource(string(page))
	if err != nil {
		t.Fatalf("extract DEFAULT_SOURCE from alyx.html: %v", err)
	}
	if got != DefaultSource {
		t.Fatalf("alyx.html DEFAULT_SOURCE differs from the engine's DefaultSource:\n--- page ---\n%s\n--- engine ---\n%s", got, DefaultSource)
	}
}

// pageDefaultSource digs the `DEFAULT_SOURCE = [ ... ].join("\n")` string
// array out of the bundled page. Two escape layers, outermost first: the
// component script is stored inside one big JSON string (the bundler's
// template blob), and each array element is a JS double-quoted literal whose
// escapes happen to be JSON-compatible — so each layer peels off with a plain
// JSON string decode.
func pageDefaultSource(page string) (string, error) {
	const open = "DEFAULT_SOURCE = ["
	const close = "].join(" // the inner "]" of []string{...} never matches this
	_, body, found := strings.Cut(page, open)
	if !found {
		return "", fmt.Errorf("marker %q not found", open)
	}
	body, _, found = strings.Cut(body, close)
	if !found {
		return "", fmt.Errorf("closing marker %q not found", close)
	}
	// peel the bundle layer: the fragment is part of a JSON string body (both
	// cut points are plain ASCII, so no escape sequence is split in half)
	var arrayText string
	if err := json.Unmarshal([]byte(`"`+body+`"`), &arrayText); err != nil {
		return "", fmt.Errorf("bundle layer is not a JSON string fragment: %v", err)
	}
	// collect the JS string literals and peel each one's own escape layer
	var lines []string
	for i := 0; i < len(arrayText); i++ {
		if arrayText[i] != '"' {
			continue
		}
		j := i + 1
		for j < len(arrayText) && arrayText[j] != '"' {
			if arrayText[j] == '\\' {
				j++ // skip the escaped char so \" does not end the literal
			}
			j++
		}
		if j >= len(arrayText) {
			return "", fmt.Errorf("unterminated string literal at offset %d", i)
		}
		var line string
		if err := json.Unmarshal([]byte(arrayText[i:j+1]), &line); err != nil {
			return "", fmt.Errorf("literal %q does not decode: %v", arrayText[i:j+1], err)
		}
		lines = append(lines, line)
		i = j
	}
	if len(lines) == 0 {
		return "", fmt.Errorf("no string literals found in the array")
	}
	return strings.Join(lines, "\n"), nil
}

func TestDefaultSourceParses(t *testing.T) {
	if _, err := parser.ParseFile(token.NewFileSet(), "sentry7.go", DefaultSource, 0); err != nil {
		t.Fatalf("DefaultSource does not parse: %v", err)
	}
}
