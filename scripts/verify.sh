#!/bin/bash
# Build, vet, gofmt-check and test this tree, writing the result to
# logs/verify.log, including each step's exit status, which terminal scrollback
# does not carry. Run it, then commit logs/verify.log and push, so the outcome
# can be read back from the repo rather than pasted from a terminal. It exists
# because the sandbox Claude Code runs in has no Go toolchain, so an upstream
# merge cannot be compiled there.

cd "$(dirname "$0")/.." || exit 1
mkdir -p logs
exec > >(tee logs/verify.log) 2>&1

echo "date:   $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "commit: $(git rev-parse HEAD)"
echo "go:     $(go version 2>&1)"
echo

if ! command -v go >/dev/null 2>&1; then
    echo "no go toolchain on this machine; nothing was verified"
    exit 127
fi

rc=0

echo "=== gofmt -l ==="
unformatted=$(gofmt -l ./cmd ./pkg ./internal 2>&1)
if [ -n "$unformatted" ]; then
    echo "$unformatted"
    echo "--- these files need gofmt ---"
    rc=1
else
    echo "clean"
fi
echo

run() {
    echo "=== $* ==="
    "$@"
    status=$?
    echo "--- exit $status ---"
    echo
    [ "$status" -eq 0 ] || rc=$status
}

run go build ./...
run go vet ./...
run go test ./...

echo "overall exit: $rc"
exit "$rc"
