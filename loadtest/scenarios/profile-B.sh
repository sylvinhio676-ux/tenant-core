#!/usr/bin/env bash
#
# Captures a CPU profile of Scenario B (loadtest/servers/tenantcore) while
# it is under Vegeta load at 2000 req/s — the specific target rate where a
# reproducible, unexplained p99 collapse was observed across 4 independent
# load-test runs (see loadtest/README.md), and that 4 black-box hypotheses
# (measurement noise, JSON encoding asymmetry, execution order,
# GOMAXPROCS/affinity mismatch) failed to explain. This captures where the
# CPU time actually goes during the anomaly, instead of continuing to guess.
#
# Requires: go, curl, vegeta (go install github.com/tsenart/vegeta@latest), taskset.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RESULTS_DIR="$REPO_ROOT/loadtest/results"
SERVER_CPU=0
PROFILE_SECONDS=15
ATTACK_DURATION="15s"
ATTACK_RATE=2000
# Same rate ladder and per-step duration as run.sh, run against the SAME
# server process before the profiled 2000 req/s step. A first attempt at
# this profile used a freshly-started server jumped straight to 2000 req/s
# and completely failed to reproduce the anomaly (p99 ~60ms instead of the
# ~1000-2000ms seen in every full run.sh run) — the anomaly may depend on
# state accumulated over the warm-up steps (heap size, GC history, cache
# state), not just the target rate in isolation. This warm-up replicates
# run.sh's actual conditions instead of a shortcut that quietly changed
# the experiment.
WARMUP_RATES=(100 500 1000)
WARMUP_DURATION="10s"
PROFILE_FILE="$RESULTS_DIR/profile-B-cpu.pprof"
VEGETA_REPORT="$RESULTS_DIR/profile-B-vegeta-report.txt"

for tool in go curl vegeta taskset; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "error: required tool '$tool' not found in PATH" >&2
		exit 1
	fi
done

mkdir -p "$RESULTS_DIR"

SERVER_PID=""
BUILD_DIR="$(mktemp -d)"

cleanup() {
	if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
	fi
	rm -rf "$BUILD_DIR"
}
trap cleanup EXIT

wait_for_server() {
	local port="$1"
	for _ in $(seq 1 50); do
		if code=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:${port}/" 2>/dev/null) && [[ -n "$code" ]]; then
			return 0
		fi
		sleep 0.1
	done
	return 1
}

echo "=== Building and starting Scenario B server (port 8082, pprof on :6061, pinned to CPU $SERVER_CPU) ==="
server_bin="$BUILD_DIR/scenario-B"
(cd "$REPO_ROOT" && go build -o "$server_bin" ./loadtest/servers/tenantcore)

taskset -c "$SERVER_CPU" "$server_bin" &
SERVER_PID=$!

if ! wait_for_server 8082; then
	echo "error: server did not become ready on port 8082" >&2
	exit 1
fi
if ! wait_for_server 6061; then
	echo "error: pprof endpoint did not become ready on port 6061" >&2
	exit 1
fi
echo "Server ready (pid $SERVER_PID)."

target_file="$(mktemp)"
{
	echo "GET http://localhost:8082/api/me"
	echo "Host: acme.localhost"
} >"$target_file"

for warmup_rate in "${WARMUP_RATES[@]}"; do
	echo "=== Warm-up: ${warmup_rate} req/s for ${WARMUP_DURATION} (same server process, no restart) ==="
	vegeta attack -targets="$target_file" -rate="${warmup_rate}" -duration="${WARMUP_DURATION}" \
		| vegeta report -type=text
done

echo "=== Starting ${PROFILE_SECONDS}s CPU profile capture (background) ==="
curl -s "http://localhost:6061/debug/pprof/profile?seconds=${PROFILE_SECONDS}" -o "$PROFILE_FILE" &
PROFILE_PID=$!

echo "=== Starting Vegeta attack: ${ATTACK_RATE} req/s for ${ATTACK_DURATION} (overlapping the profile capture) ==="
vegeta attack -targets="$target_file" -rate="${ATTACK_RATE}" -duration="${ATTACK_DURATION}" \
	| vegeta report -type=text | tee "$VEGETA_REPORT"

echo "=== Waiting for CPU profile capture to finish ==="
wait "$PROFILE_PID"

rm -f "$target_file"

echo "=== Stopping server ==="
cleanup
trap - EXIT

if [[ ! -s "$PROFILE_FILE" ]]; then
	echo "error: $PROFILE_FILE is empty or missing — profile capture likely failed" >&2
	exit 1
fi

echo
echo "=================================================================="
echo " CPU profile saved to: $PROFILE_FILE"
echo " Vegeta report saved to: $VEGETA_REPORT"
echo " Top functions by cumulative CPU time:"
echo "=================================================================="
go tool pprof -top -cum "$PROFILE_FILE"
