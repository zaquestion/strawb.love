// smoke.test.js — end-to-end proof that the COMMITTED artifacts work: loads
// the freshly built wasm with the freshly copied ../wasm_exec.js and drives
// the engineCall JSON protocol through the full story — boot, rigged break,
// admin buff (sentryWake), spawn escalation, each canonical solution, restore
// round trip, rejection messages. Run by build.sh: node smoke.test.js <wasm>.
//
// This re-proves in wasm what patch_test.go proves natively; a divergence here
// means a js/wasm-only bug (syscall/js marshalling, embed, exports wiring).
"use strict";
const fs = require("fs");
const path = require("path");

// The SAME glue the page ships — testing against a different wasm_exec.js
// would validate nothing.
require(path.join(__dirname, "..", "..", "wasm_exec.js"));

const wasmFile = process.argv[2];
if (!wasmFile) {
  console.error("usage: node smoke.test.js <game.wasm>");
  process.exit(2);
}

let failures = 0;
function assert(cond, label, detail) {
  if (cond) {
    console.log("  ok  " + label);
  } else {
    failures++;
    console.error("  FAIL " + label + (detail !== undefined ? " — " + JSON.stringify(detail) : ""));
  }
}

// Mirrors engine.RiggedPickStrength — the act-1 factory pick hp. The boot
// assertions below fail loudly if the engine's rigging drifts from this.
const RIGGED_HP = 3;

let id = 0;
function call(action, args) {
  const m = Object.assign({ id: ++id, type: "act", action }, args || {});
  const r = JSON.parse(globalThis.engineCall(JSON.stringify(m)));
  if (r.type === "crash") throw new Error("engine crash on " + action + ": " + r.error);
  if (r.id !== id) throw new Error(action + ": reply id " + r.id + " != " + id);
  return r;
}

// pickAll pushes force 50 at every unset pin. With forgiveness cranked to 40
// this always sets (targets live in 25..85, so |50-t| <= 35 <= 40, and 50 can
// never overset because t+40 >= 65 > 50). Returns the final (win-gate) reply.
function pickAll(state) {
  let r;
  state.lock.pins.forEach((p, i) => {
    if (!p.set) r = call("pushPin", { pin: i, force: 50 });
  });
  return r;
}

const defaultSource = fs
  .readFileSync(path.join(__dirname, "..", "sentry", "sentry7.go"), "utf8")
  .replace(/\n$/, ""); // the editor copy carries no trailing newline
function editedSource(anchor, replacement) {
  if (!defaultSource.includes(anchor)) throw new Error("edit anchor drifted: " + anchor);
  return defaultSource.replace(anchor, replacement);
}

// hackIn: boot → act 2 → cranked params (waking the sentry) — the state every
// solution starts from.
function hackIn() {
  call("boot", {});
  call("setUser", { user: "alyx" });
  call("setAct", { act: 2 });
  const wake = call("setParam", { name: "pickStrength", value: 9000 });
  const calm = call("setParam", { name: "forgiveness", value: 40 });
  return { wake, calm };
}

async function main() {
  const go = new Go();
  const t0 = Date.now();
  const { instance } = await WebAssembly.instantiate(fs.readFileSync(wasmFile), go.importObject);
  go.run(instance); // never resolves: wasmmain parks in select{}
  while (!globalThis.engineCall) await new Promise((r) => setTimeout(r, 10));
  console.log("boot: engineCall ready in " + (Date.now() - t0) + "ms");

  console.log("— boot state —");
  let r = call("boot", {});
  assert(r.state.act === 1 && !r.state.won && !r.state.sentryAwake, "factory act-1 state", r.state);
  assert(r.state.params.pickStrength === RIGGED_HP && r.state.params.forgiveness === 1, "rigged params {" + RIGGED_HP + ",1}", r.state.params);
  assert(r.state.lock.pins.length === 5 && r.state.lock.serial === 1, "lock #1, five pins", r.state.lock);
  assert(r.state.pick.hp === RIGGED_HP && !r.state.pick.broken, "pick at " + RIGGED_HP + " hp", r.state.pick);
  assert(typeof r.state.snapshot === "string" && r.state.snapshot.length > 0, "snapshot rides every reply");
  assert(!r.state.lastHookError, "default sentry7 source eval'd clean at boot", r.state.lastHookError);

  console.log("— act 1: the rig —");
  call("setUser", { user: "alyx" });
  // Force 0 is always a slip (targets ≥ 25, window ±1): RIGGED_HP misses snap the pick.
  for (let miss = 1; miss < RIGGED_HP; miss++) {
    r = call("pushPin", { pin: 0, force: 0 });
    assert(!r.event && r.state.pick.hp === RIGGED_HP - miss, "miss " + miss + " chips, no drama yet", r.state.pick);
  }
  r = call("pushPin", { pin: 0, force: 0 });
  assert(r.event === "pickBroke" && r.state.pick.broken, "miss " + RIGGED_HP + " snaps the pick", r);
  r = call("pushPin", { pin: 0, force: 50 });
  assert(r.push.result === "already" && !r.event, "broken pick refuses pushes quietly", r.push);
  r = call("newLock", {});
  assert(!r.state.pick.broken && r.state.pick.hp === RIGGED_HP && r.state.lock.serial === 2, "newLock re-cuts lock AND pick", r.state);

  console.log("— act 2: admin buff wakes SENTRY-7 —");
  const { wake, calm } = hackIn();
  assert(wake.event === "sentryWake" && wake.state.sentryAwake, "first param write wakes the sentry", wake.event);
  assert(wake.state.params.pickStrength === 9000 && wake.state.pick.hp === 9000, "the write still lands (buff + banner, same beat)", wake.state);
  assert(calm.event === "", "sentry only wakes once", calm.event);

  r = pickAll(call("newLock", {}).state);
  assert(r.event === "sentrySpawned" && !r.state.won, "picking as an intruder spawns a fresh lock", r.event);
  assert(r.state.locksInstalled === 1 && !r.state.lock.picked, "locksInstalled counts the drama", r.state.locksInstalled);
  assert((r.hookLog || []).some((l) => l.includes("INTRUDER: alyx")), "hookLog carries the sentry's pick-time line", r.hookLog);
  const spawnSerial = r.state.lock.serial;
  r = pickAll(r.state);
  assert(r.event === "sentrySpawned" && r.state.locksInstalled === 2 && r.state.lock.serial > spawnSerial, "every pick spawns lock #N+1", r.state);

  console.log("— act 3, solution 1: stop making locks —");
  hackIn();
  r = call("runCode", { source: editedSource("var NewLocksPerPick = 1", "var NewLocksPerPick = 0") });
  assert(r.run && r.run.ok && r.state.patched, "patch loads and goes live", r.run);
  r = pickAll(call("newLock", {}).state);
  assert(r.event === "vaultOpen" && r.state.won, "locks=0 opens the vault", r.event);
  assert((r.hookLog || []).some((l) => l.includes("installing 0 new lock(s)")), "her code ran and said so", r.hookLog);

  console.log("— act 3, solution 2: pre-picked locks —");
  hackIn();
  r = call("runCode", {
    source: editedSource(
      "lock.Picked = false // a lock that starts picked would be useless. obviously.",
      "lock.Picked = true"
    ),
  });
  assert(r.run && r.run.ok, "patch loads", r.run);
  r = pickAll(call("newLock", {}).state);
  assert(r.event === "vaultOpen" && r.state.won, "pre-picked installs pop open", r.event);

  console.log("— act 3, solution 3: whitelist alyx —");
  hackIn();
  const whitelist = editedSource('var Authorized = []string{"zaq"}', 'var Authorized = []string{"zaq", "alyx"}');
  r = call("runCode", { source: whitelist });
  assert(r.run && r.run.ok, "patch loads", r.run);
  r = pickAll(call("newLock", {}).state);
  assert(r.event === "vaultOpen" && r.state.won, "whitelisted user opens the vault", r.event);
  assert((r.hookLog || []).includes("hi alyx . carry on."), "it says hi to her", r.hookLog);
  const wonSnapshot = r.state.snapshot;

  console.log("— watchdog respawn: restore round trip —");
  r = call("boot", {});
  assert(!r.state.won, "fresh boot forgets everything", r.state.won);
  r = call("restore", { snapshot: wonSnapshot, source: whitelist });
  assert(r.state.snapshot === wonSnapshot, "restore reproduces the snapshot byte-for-byte");
  assert(r.state.won && r.state.patched, "won + patched survive the respawn", r.state);
  r = call("restore", { snapshot: "{corrupt", source: "" });
  assert(r.state.act === 1 && !r.state.won && r.state.lock.serial === 1, "corrupt snapshot falls back to factory", r.state);

  console.log("— rejections —");
  r = call("runCode", { source: "package sentry\n\nfunc OnLockPicked(user string) {" });
  assert(r.run && !r.run.ok && r.run.error.length > 0, "syntax error surfaces a message", r.run);
  r = call("runCode", { source: "package sentry\n\nvar NewLocksPerPick = 0" });
  assert(
    r.run && !r.run.ok && r.run.error === "SENTRY-7 patch rejected: OnLockPicked is missing — it needs that to boot.",
    "missing hook gets the pinned line",
    r.run && r.run.error
  );
  const t1 = Date.now();
  r = call("runCode", { source: defaultSource });
  console.log("  (runCode of pristine source: " + (Date.now() - t1) + "ms)");
  assert(r.run && r.run.ok, "pristine source runs clean", r.run);

  if (failures > 0) {
    console.error("\nSMOKE FAILED: " + failures + " assertion(s)");
    process.exit(1);
  }
  console.log("\nsmoke: all assertions passed");
  process.exit(0);
}

main().catch((e) => {
  console.error("SMOKE CRASHED:", e);
  process.exit(1);
});
