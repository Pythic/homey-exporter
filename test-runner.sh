#!/usr/bin/env bash
# Usage: ./test-runner.sh [-v] [-c] [-r]   -v verbose, -c coverage, -r race detector
set -euo pipefail
cd "$(dirname "$0")"
args=()
while getopts "vcr" o; do
	case $o in
	v) args+=(-v) ;;
	c) args+=(-coverprofile=coverage.out) ;;
	r) args+=(-race) ;;
	*) exit 2 ;;
	esac
done
go vet ./...
go test "${args[@]}" ./...
[[ -f coverage.out ]] && go tool cover -func=coverage.out | tail -1
