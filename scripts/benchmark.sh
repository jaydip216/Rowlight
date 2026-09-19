#!/bin/sh
set -eu

binary=${1:-./bin/rowlight}
port=${ROWLIGHT_BENCH_PORT:-7089}
runs=${ROWLIGHT_BENCH_RUNS:-7}
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/rowlight-benchmark.XXXXXX")
pid=

if [ ! -x "$binary" ]; then
  echo "Rowlight binary not found at $binary; run make build first." >&2
  exit 1
fi

cleanup() {
  if [ -n "$pid" ]; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT INT TERM

run=1
while [ "$run" -le "$runs" ]; do
  run_port=$((port + run - 1))
  log_file="$work_dir/run-$run.log"
  start=$(perl -MTime::HiRes=time -e 'printf "%.6f", time')
  "$binary" -no-open -port "$run_port" >"$log_file" 2>&1 &
  pid=$!

  attempt=0
  while ! curl -fsS "http://127.0.0.1:$run_port/" >/dev/null 2>&1; do
    if ! kill -0 "$pid" 2>/dev/null; then
      cat "$log_file" >&2
      exit 1
    fi
    attempt=$((attempt + 1))
    if [ "$attempt" -ge 500 ]; then
      echo "Rowlight did not become ready within 5 seconds." >&2
      exit 1
    fi
    sleep 0.01
  done
  end=$(perl -MTime::HiRes=time -e 'printf "%.6f", time')
  perl -e 'printf "%.1f\n", ($ARGV[1] - $ARGV[0]) * 1000' "$start" "$end" >>"$work_dir/startup"
  rss_kib=$(ps -o rss= -p "$pid" | tr -d ' ')
  perl -e 'printf "%.1f\n", $ARGV[0] / 1024' "$rss_kib" >>"$work_dir/rss"
  kill "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
  pid=
  run=$((run + 1))
done

sort -n "$work_dir/startup" >"$work_dir/startup.sorted"
sort -n "$work_dir/rss" >"$work_dir/rss.sorted"
median_position=$(((runs + 1) / 2))
p95_position=$(((95 * runs + 99) / 100))
startup_median=$(sed -n "${median_position}p" "$work_dir/startup.sorted")
startup_p95=$(sed -n "${p95_position}p" "$work_dir/startup.sorted")
rss_median=$(sed -n "${median_position}p" "$work_dir/rss.sorted")

printf 'runs=%s\n' "$runs"
printf 'startup_ms_samples=%s\n' "$(paste -sd, "$work_dir/startup")"
printf 'startup_ms_median=%s\n' "$startup_median"
printf 'startup_ms_p95=%s\n' "$startup_p95"
printf 'idle_rss_mib_samples=%s\n' "$(paste -sd, "$work_dir/rss")"
printf 'idle_rss_mib_median=%s\n' "$rss_median"

env GOCACHE="${GOCACHE:-/tmp/rowlight-gocache}" GOMODCACHE="${GOMODCACHE:-/tmp/rowlight-gomodcache}" \
  go test -run '^$' -bench '^BenchmarkEncodeLargeResult$' -benchmem ./internal/store
