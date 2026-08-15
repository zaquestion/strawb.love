// worker.js — vault engine bootstrap. The main thread sends a compiled
// WebAssembly.Module once ({type:"init"}), then "act" messages; every act is
// forwarded to the Go side's synchronous engineCall (JSON string in, JSON
// string out) and the reply posted back. The main thread watchdogs every
// call and will terminate+respawn this whole worker on a missed deadline, so
// nothing here needs to defend against a hung engineCall — it CAN'T return
// control if player code loops forever, and that is the watchdog's job.
importScripts("wasm_exec.js");

let ready = false;
let queue = []; // acts that arrived while wasm was still instantiating (order preserved)

onmessage = async (e) => {
  const m = e.data;
  if (m.type === "init") {
    try {
      const go = new Go();
      const inst = await WebAssembly.instantiate(m.module, go.importObject);
      // go.run never resolves (main parks in select{}); a rejection means the
      // runtime died, which main treats exactly like a watchdog timeout.
      go.run(inst).catch((err) => postMessage({ type: "crash", error: String(err) }));
      // engineCall appears a beat after run() starts — poll instead of racing it.
      while (!globalThis.engineCall) await new Promise((r) => setTimeout(r, 10));
      ready = true;
      postMessage({ id: m.id, type: "ready" });
      for (const q of queue) handle(q);
      queue = [];
    } catch (err) {
      postMessage({ type: "crash", error: String((err && err.message) || err) });
    }
    return;
  }
  if (!ready) { queue.push(m); return; }
  handle(m);
};

function handle(m) {
  try {
    postMessage(JSON.parse(globalThis.engineCall(JSON.stringify(m))));
  } catch (err) {
    // A Go panic that escapes HandleSafe surfaces as a JS exception here;
    // main recovers via terminate → respawn → snapshot restore.
    postMessage({ type: "crash", error: String((err && err.message) || err) });
  }
}
