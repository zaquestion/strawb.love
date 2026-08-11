#!/usr/bin/env bash
# build.sh — gate, build, and package the vault engine.
#
# Every step is a gate: fmt, vet, native tests (the full yaegi solution
# matrix runs natively), THEN the js/wasm build, THEN the node smoke test
# against the freshly built wasm + freshly copied glue. The gh-pages deploy
# never runs Go — the artifacts this script writes are COMMITTED files:
#
#   ../alyx/game.wasm.gz   gzip -9 of the wasm (gh-pages won't compress .wasm;
#                          the page inflates with DecompressionStream)
#   ../alyx/wasm_exec.js   copied verbatim from $GOROOT/lib/wasm/
#
# (../alyx/worker.js is hand-written and committed, not generated here.)
set -euo pipefail
cd "$(dirname "$0")"

GZ_BUDGET_BYTES=3500000 # bytes: the slim yaegi build lands ~2.6MB gz; past 3.5MB something regressed (stdlib.Symbols creeping in?)

echo "── gofmt ──"
unformatted=$(gofmt -l .)
if [ -n "$unformatted" ]; then
  echo "gofmt needed on:" >&2
  echo "$unformatted" >&2
  exit 1
fi

echo "── go vet ──"
go vet ./...
(cd gamefacade && go vet ./...) # nested module: ./... above does not reach it

echo "── go test ──"
go test ./...

echo "── build js/wasm ──"
wasm_out=$(mktemp -t game.wasm.XXXXXX)
trap 'rm -f "$wasm_out"' EXIT
GOOS=js GOARCH=wasm go build -trimpath -ldflags="-s -w" -o "$wasm_out" ./wasmmain
mkdir -p ../alyx
gzip -9 -c "$wasm_out" > ../alyx/game.wasm.gz
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" ../alyx/wasm_exec.js

echo "── node smoke (drives the real wasm + shipped glue) ──"
node smoke/smoke.test.js "$wasm_out"

gz_size=$(wc -c < ../alyx/game.wasm.gz)
echo "── artifacts ──"
ls -la ../alyx/game.wasm.gz ../alyx/wasm_exec.js ../alyx/worker.js
echo "game.wasm.gz: ${gz_size} bytes gzipped"
if [ "$gz_size" -gt "$GZ_BUDGET_BYTES" ]; then
  echo "WARN: game.wasm.gz exceeds the ${GZ_BUDGET_BYTES}-byte budget" >&2
fi
echo "build: OK"
