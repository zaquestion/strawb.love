//go:build js && wasm

// Command wasmmain is the worker-side entry point: it registers
// globalThis.engineCall — a synchronous JSON-string-in/JSON-string-out
// bridge to one patch.Session — and parks forever. The worker (alyx/worker.js)
// forwards every "act" postMessage through engineCall and posts the reply
// back to the main thread.
package main

import (
	"syscall/js"

	"strawb.love/game/patch"
)

func main() {
	s := patch.NewSession()
	js.Global().Set("engineCall", js.FuncOf(func(this js.Value, args []js.Value) any {
		if len(args) != 1 {
			// A malformed call is a worker bug, not a player action; reply in
			// the crash shape the main thread already knows how to recover from.
			return `{"type":"crash","error":"engineCall wants exactly one JSON string"}`
		}
		// HandleSafe never panics: engine bugs degrade to a crash reply and
		// one lost turn instead of killing the wasm instance.
		return s.HandleSafe(args[0].String())
	}))
	// Park forever: if main returned, the Go runtime would exit and every
	// js.FuncOf callback would start throwing "Go program has already exited".
	select {}
}
