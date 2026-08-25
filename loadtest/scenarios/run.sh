#!/usr/bin/env bash
#
# Load test orchestration for tenant-core, following a layered
# decomposition methodology (see loadtest/README.md): the three servers
# under loadtest/servers/ (A: pure net/http baseline, B: tenant-core
# without RBAC, C: tenant-core with RBAC) are load-tested independently,
# in isolation, at a series of increasing target rates. Comparing
# scenarios lets you attribute a cost to a specific layer instead of
# guessing from one opaque "requests/sec" number.
#
# Single-machine protocol: the server and Vegeta itself both run on this
# machine. taskset pins the SERVER ONLY to CPU 0, so it always runs in a
# fixed, comparable environment across scenarios A/B/C.
#
# Vegeta itself is deliberately left UNPINNED — this was verified
# empirically, not assumed: an earlier version of this script also pinned
# Vegeta to CPU 1, on the reasonable-sounding theory that symmetric
# isolation would help. In practice it broke the generator instead of
# helping it — pinned to a single CPU, Vegeta's own worker pool couldn't
# sustain its own request schedule (a 500 req/s, 10s attack actually
# stopped emitting requests after ~2.3s instead of running the full
# window), producing exactly the kind of misleading, non-monotonic
# numbers this script exists to avoid. The same attack against the same
# (still CPU-0-pinned) server, with Vegeta unpinned, completed cleanly:
# full 10s window, 5000/5000 requests, p99 3.9ms. See loadtest/README.md's
# "Known limitation of this protocol" section for the full account before
# drawing any conclusion from these numbers, especially anything
# resembling a production capacity claim.
#
# Requires: go, curl, vegeta (go install github.com/tsenart/vegeta@latest),
# jq, taskset (util-linux — normally preinstalled on Linux; used only to
# pin the server).
#
# Usage:
#   ./run.sh              # run all three scenarios (A, B, C)
#   ./run.sh A            # run only scenario A (or B, or C)
#   ./run.sh B run2       # run only scenario B, saving to scenario-B-run2-*.txt
#                         # instead of scenario-B-*.txt — use this to repeat a
#                         # scenario without overwriting a previous run, e.g.
#                         # to check whether an odd result reproduces.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
RESULTS_DIR="$REPO_ROOT/loadtest/results"
DURATION="10s"
# Kept deliberately modest for a dual-core, single-machine setup where one
# core is dedicated to the server and one to the generator: 10000+ req/s
# targets have no realistic chance of being reached under that
# constraint, and only produce noise (see loadtest/README.md).
RATES=(100 500 1000 2000 3000)
SERVER_CPU=0
# taskset restricts the process's CPU affinity mask at the OS level, which
# the Go runtime reads via sched_getaffinity at startup to auto-derive
# GOMAXPROCS — so this should already be redundant with the taskset
# pinning above. Set explicitly anyway, as a targeted diagnostic: an
# investigation into a reproducible latency anomaly specific to Scenario B
# (see docs/ARCHITECTURE.md / loadtest/README.md) needs to rule out any
# mismatch between the two mechanisms before looking elsewhere.
SERVER_GOMAXPROCS=1

SCENARIO_FILTER="${1:-}"
RUN_LABEL="${2:-}"
if [[ -n "$SCENARIO_FILTER" && ! "$SCENARIO_FILTER" =~ ^[ABC]$ ]]; then
	echo "error: invalid scenario '$SCENARIO_FILTER' — expected A, B, or C" >&2
	exit 1
fi
if [[ -n "$RUN_LABEL" && -z "$SCENARIO_FILTER" ]]; then
	echo "error: a run label requires a specific scenario (./run.sh <A|B|C> <label>)" >&2
	exit 1
fi

for tool in go curl vegeta jq taskset; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "error: required tool '$tool' not found in PATH" >&2
		case "$tool" in
		vegeta)
			echo "  install with: go install github.com/tsenart/vegeta@latest" >&2
			;;
		taskset)
			echo "  taskset ships with util-linux, normally preinstalled on Linux." >&2
			echo "  Debian/Ubuntu: sudo apt install util-linux" >&2
			echo "  Fedora/RHEL:   sudo dnf install util-linux" >&2
			echo "  Arch:          sudo pacman -S util-linux" >&2
			echo "  Not available on macOS (no taskset equivalent in this script)." >&2
			;;
		esac
		exit 1
	fi
done

CPU_COUNT=$(nproc)
if (( CPU_COUNT < 2 )); then
	echo "error: this protocol pins the server to CPU 0 so it stays isolated" >&2
	echo "  from Vegeta and the rest of the system, which needs at least one" >&2
	echo "  other CPU to run on (found: $CPU_COUNT total)" >&2
	exit 1
fi

mkdir -p "$RESULTS_DIR"

# name | server package (relative to repo root) | port | path | Host header (empty = none)
SCENARIOS=(
	"A|loadtest/servers/httponly|8081|/ping|"
	"B|loadtest/servers/tenantcore|8082|/api/me|acme.localhost"
	"C|loadtest/servers/rbac|8083|/api/users|acme.localhost"
)

SERVER_PID=""
BUILD_DIR="$(mktemp -d)"

cleanup_server() {
	if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
		kill "$SERVER_PID" 2>/dev/null || true
		wait "$SERVER_PID" 2>/dev/null || true
	fi
	SERVER_PID=""
}

cleanup_all() {
	cleanup_server
	rm -rf "$BUILD_DIR"
}
trap cleanup_all EXIT

# wait_for_server polls until the server on $1 accepts a connection and
# returns any HTTP status code — it does not need to be the real route,
# just proof the listener is up.
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

declare -a SUMMARY_ROWS=()

for entry in "${SCENARIOS[@]}"; do
	IFS='|' read -r name dir port path host <<<"$entry"

	if [[ -n "$SCENARIO_FILTER" && "$name" != "$SCENARIO_FILTER" ]]; then
		continue
	fi

	echo "=== Scenario $name: building and starting server (port $port, pinned to CPU $SERVER_CPU) ==="
	server_bin="$BUILD_DIR/scenario-$name"
	(cd "$REPO_ROOT" && go build -o "$server_bin" "./$dir")

	GOMAXPROCS="$SERVER_GOMAXPROCS" taskset -c "$SERVER_CPU" "$server_bin" &
	SERVER_PID=$!

	if ! wait_for_server "$port"; then
		echo "error: scenario $name server did not become ready on port $port" >&2
		cleanup_server
		exit 1
	fi
	echo "Scenario $name server ready (pid $SERVER_PID)."

	target_file="$(mktemp)"
	{
		echo "GET http://localhost:${port}${path}"
		if [[ -n "$host" ]]; then
			echo "Host: ${host}"
		fi
	} >"$target_file"

	for rate in "${RATES[@]}"; do
		echo "--- Scenario $name @ ${rate} req/s for ${DURATION} (server pinned to CPU $SERVER_CPU, generator unpinned) ---"

		# Live CPU profile capture: Scenario B's 2000 req/s step reproducibly
		# showed an unexplained p99 collapse (~1000-2000ms, vs ~10ms for
		# Scenario C) across 4 independent runs of this exact sequence. A
		# standalone reproduction attempt (loadtest/scenarios/profile-B.sh),
		# even after replicating the 100/500/1000 req/s warm-up, only
		# partially reproduced it (p99 ~170ms) — so this captures a profile
		# from an actual run.sh execution instead, to be certain the exact
		# conditions match.
		PROFILE_PID=""
		if [[ "$name" == "B" && "$rate" -eq 2000 ]]; then
			echo "Capturing a live 10s CPU profile of Scenario B during this step -> $RESULTS_DIR/profile-B-live-2000rps.pprof"
			curl -s "http://localhost:6061/debug/pprof/profile?seconds=10" -o "$RESULTS_DIR/profile-B-live-2000rps.pprof" &
			PROFILE_PID=$!
		fi

		result_bin="$(mktemp)"
		vegeta attack -targets="$target_file" -rate="${rate}" -duration="$DURATION" >"$result_bin"

		if [[ -n "$PROFILE_PID" ]]; then
			wait "$PROFILE_PID"
			echo "CPU profile capture finished."
		fi

		if [[ -n "$RUN_LABEL" ]]; then
			text_report="$RESULTS_DIR/scenario-${name}-${RUN_LABEL}-${rate}rps.txt"
		else
			text_report="$RESULTS_DIR/scenario-${name}-${rate}rps.txt"
		fi
		vegeta report -type=text <"$result_bin" >"$text_report"

		json_report=$(vegeta report -type=json <"$result_bin")
		actual_rate=$(echo "$json_report" | jq -r '.throughput')
		success=$(echo "$json_report" | jq -r '.success')
		p99_ns=$(echo "$json_report" | jq -r '.latencies["99th"]')
		# LC_ALL=C so awk always renders a '.' decimal point, regardless of
		# the system locale (observed producing "1620,10" under fr_FR,
		# which would silently corrupt anything re-parsing this output).
		p99_ms=$(LC_ALL=C awk "BEGIN { printf \"%.2f\", ${p99_ns:-0} / 1000000 }")
		success_pct=$(LC_ALL=C awk "BEGIN { printf \"%.1f\", ${success:-0} * 100 }")

		rm -f "$result_bin"

		echo "Saved: $text_report (actual ${actual_rate} req/s, success ${success_pct}%, p99 ${p99_ms}ms)"
		SUMMARY_ROWS+=("$name|$rate|$actual_rate|$success_pct|$p99_ms")
	done

	rm -f "$target_file"

	echo "=== Scenario $name: stopping server ==="
	cleanup_server
	echo
done

echo "=================================================================="
echo " Comparative summary"
echo ""
echo " WARNING: Single-machine test (dual-core). The server is CPU-pinned"
echo " (CPU 0) for a fixed, comparable environment across scenarios;"
echo " Vegeta itself is deliberately left unpinned — pinning it was tried"
echo " and empirically broke it (see run.sh header and README). This"
echo " measures relative behavior between scenarios A/B/C, NOT an"
echo " absolute production capacity figure. A real capacity benchmark"
echo " requires generator and server on separate machines."
echo ""
echo " Rule: never quote a req/s number on its own. Always state the"
echo " machine, the exact scenario, and the success rate/p99 alongside"
echo " it — see loadtest/README.md and the existing Go-level benchmarks"
echo " in this repo (e.g. ratelimit/ratelimit_bench_test.go) for the same"
echo " discipline applied at the micro-benchmark level."
echo "=================================================================="
printf "%-10s %-12s %-14s %-10s %-10s\n" "Scenario" "Target rps" "Actual rps" "Success" "p99 (ms)"
for row in "${SUMMARY_ROWS[@]}"; do
	IFS='|' read -r name rate actual_rate success_pct p99_ms <<<"$row"
	printf "%-10s %-12s %-14s %-10s %-10s\n" "$name" "$rate" "$actual_rate" "${success_pct}%" "$p99_ms"
done
