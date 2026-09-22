#!/usr/bin/env bash
# Exercise admitted login capacity and deliberate overload against the assembled
# service. HTTP 200/401 are admitted work; HTTP 429/503 are load shedding.
set -euo pipefail

duration="${BLOOM_LOAD_DURATION_SECONDS:-10}"
capacity_concurrency="${BLOOM_LOAD_CONCURRENCY:-4}"
overload_concurrency="${BLOOM_LOAD_OVERLOAD_CONCURRENCY:-32}"
target_rps="${BLOOM_LOAD_TARGET_RPS:-8}"
target_p95_ms="${BLOOM_LOAD_TARGET_P95_MS:-1000}"
target_error_pct="${BLOOM_LOAD_TARGET_ERROR_PCT:-1}"
target_rss_bytes="${BLOOM_LOAD_TARGET_RSS_BYTES:-268435456}"
postgres_dsn="${BLOOM_LOAD_POSTGRES_DSN:-}"
password="bloom-loadtest-2026-safe"
username="load-user"
secret="0123456789abcdef0123456789abcdef"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
workdir="$(mktemp -d)"
binary="${workdir}/bloom"
pids=()
targets_failed=0

cleanup() {
  local pid
  for pid in "${pids[@]:-}"; do
    kill "${pid}" 2>/dev/null || true
    wait "${pid}" 2>/dev/null || true
  done
  rm -rf -- "${workdir}"
}
trap cleanup EXIT

require_positive_integer() {
  local name="$1" value="$2"
  if [[ ! "${value}" =~ ^[1-9][0-9]*$ ]]; then
    echo "${name} must be a positive integer" >&2
    exit 2
  fi
}

wait_ready() {
  local base_url="$1" pid="$2" attempt
  for attempt in $(seq 1 100); do
    if curl --silent --fail "${base_url}/readyz" >/dev/null; then
      return
    fi
    if ! kill -0 "${pid}" 2>/dev/null; then
      echo "Bloom exited before readiness" >&2
      exit 1
    fi
    sleep 0.1
  done
  echo "Bloom did not become ready" >&2
  exit 1
}

sample_peak_rss() {
  local pid="$1" peak_file="$2" sample peak=0
  for _ in $(seq 1 $((duration * 40 + 400))); do
    [[ -r "/proc/${pid}/status" ]] || break
    sample="$(awk '/VmRSS:/ {print $2 * 1024}' "/proc/${pid}/status")"
    if [[ -n "${sample}" ]] && (( sample > peak )); then
      peak="${sample}"
      echo "${peak}" >"${peak_file}"
    fi
    sleep 0.025
  done
}

login_request() {
  local base_url="$1" payload="$2" forwarded="$3" header_file="$4"
  local result retry
  result="$(curl --silent --show-error --output /dev/null --dump-header "${header_file}" \
    --header 'Content-Type: application/json; charset=utf-8' \
    --header "X-Forwarded-For: ${forwarded}" --data "${payload}" \
    --write-out '%{http_code} %{time_total}' "${base_url}/api/v1/auth/login" || echo '000 0')"
  retry="$(awk 'tolower($1) == "retry-after:" {gsub("\r", "", $2); value=$2} END {print value}' "${header_file}")"
  printf '%s %s\n' "${result}" "${retry:--}"
}

capacity_worker() {
  local base_url="$1" output="$2" worker="$3" requests="$4" sequence payload address
  local headers="${output}.headers"
  for sequence in $(seq 1 "${requests}"); do
    payload="{\"username\":\"missing-${worker}-${sequence}\",\"password\":\"wrong-password-value\"}"
    address="2001:db8:${worker}:${sequence}::1"
    login_request "${base_url}" "${payload}" "${address}" "${headers}" >>"${output}"
  done
}

overload_worker() {
  local base_url="$1" output="$2" requests="$3" sequence payload
  local headers="${output}.headers"
  payload="{\"username\":\"${username}\",\"password\":\"wrong-password-value\"}"
  for sequence in $(seq 1 "${requests}"); do
    login_request "${base_url}" "${payload}" "198.51.100.10" "${headers}" >>"${output}"
  done
}

run_phase() {
  local phase="$1" base_url="$2" concurrency="$3" requests="$4" result_file="$5"
  local worker worker_pid start_ns end_ns elapsed_file="${result_file}.elapsed" worker_pids=()
  start_ns="$(date +%s%N)"
  for worker in $(seq 1 "${concurrency}"); do
    if [[ "${phase}" == capacity ]]; then
      capacity_worker "${base_url}" "${result_file}.worker-${worker}" "${worker}" "${requests}" &
    else
      overload_worker "${base_url}" "${result_file}.worker-${worker}" "${requests}" &
    fi
    worker_pids+=("$!")
  done
  for worker_pid in "${worker_pids[@]}"; do
    wait "${worker_pid}"
  done
  end_ns="$(date +%s%N)"
  awk -v start="${start_ns}" -v end="${end_ns}" 'BEGIN {printf "%.6f\n", (end-start)/1000000000}' >"${elapsed_file}"
  : >"${result_file}"
  for worker in $(seq 1 "${concurrency}"); do
    append_worker_results "${result_file}.worker-${worker}" "${result_file}"
  done
}

append_worker_results() {
  local worker_file="$1" result_file="$2"
  if [[ -f "${worker_file}" ]]; then
    sort -s -k1,1 "${worker_file}" >>"${result_file}"
  fi
}

percentile_ms() {
  local input="$1" count="$2" percentile="$3" rank
  if (( count == 0 )); then
    echo "0"
    return
  fi
  rank=$(((count * percentile + 99) / 100))
  sed -n "${rank}p" "${input}" | awk '{printf "%.3f", $1 * 1000}'
}

phase_stats() {
  local result_file="$1" prefix="$2" latency_file
  local total admitted limited overloaded other invalid_retry p95
  latency_file="${result_file}.latency"
  total="$(wc -l <"${result_file}")"
  admitted="$(awk '$1 == 200 || $1 == 401 {count++} END {print count+0}' "${result_file}")"
  limited="$(awk '$1 == 429 {count++} END {print count+0}' "${result_file}")"
  overloaded="$(awk '$1 == 503 {count++} END {print count+0}' "${result_file}")"
  other="$((total - admitted - limited - overloaded))"
  invalid_retry="$(awk '($1 == 429 || $1 == 503) && $3 !~ /^[1-9][0-9]*$/ {count++} END {print count+0}' "${result_file}")"
  awk '$1 == 200 || $1 == 401 {print $2}' "${result_file}" | sort -n >"${latency_file}"
  p95="$(percentile_ms "${latency_file}" "${admitted}" 95)"
  printf -v "${prefix}_total" '%s' "${total}"
  printf -v "${prefix}_admitted" '%s' "${admitted}"
  printf -v "${prefix}_limited" '%s' "${limited}"
  printf -v "${prefix}_overloaded" '%s' "${overloaded}"
  printf -v "${prefix}_other" '%s' "${other}"
  printf -v "${prefix}_invalid_retry" '%s' "${invalid_retry}"
  printf -v "${prefix}_p95" '%s' "${p95}"
}

report_engine() {
  local engine="$1" capacity_file="$2" overload_file="$3" peak_file="$4"
  local elapsed rps error_pct peak capacity_met overload_met
  phase_stats "${capacity_file}" capacity
  phase_stats "${overload_file}" overload
  elapsed="$(<"${capacity_file}.elapsed")"
  peak="$(<"${peak_file}")"
  rps="$(awk -v total="${capacity_total}" -v seconds="${elapsed}" 'BEGIN {printf "%.2f", total/seconds}')"
  error_pct="$(awk -v errors="$((capacity_limited + capacity_overloaded + capacity_other))" -v total="${capacity_total}" 'BEGIN {printf "%.2f", total ? errors*100/total : 100}')"
  capacity_met="$(awk -v rps="${rps}" -v min="${target_rps}" -v p95="${capacity_p95}" -v maxp="${target_p95_ms}" -v errors="${error_pct}" -v maxe="${target_error_pct}" -v rss="${peak}" -v maxr="${target_rss_bytes}" 'BEGIN {print (rps>=min && p95<=maxp && errors<=maxe && rss<=maxr) ? "yes" : "no"}')"
  overload_met="$(awk -v limited="${overload_limited}" -v overloaded="${overload_overloaded}" -v retry="${overload_invalid_retry}" -v other="${overload_other}" -v p95="${overload_p95}" -v maxp="${target_p95_ms}" 'BEGIN {print (limited>0 && overloaded>0 && retry==0 && other==0 && p95<=maxp) ? "yes" : "no"}')"
  printf 'engine=%s phase=capacity concurrency=%s requests=%s rps=%s admitted=%s p95_ms=%s error_rate_pct=%s peak_rss_bytes=%s status_429=%s status_503=%s other=%s target_met=%s\n' \
    "${engine}" "${capacity_concurrency}" "${capacity_total}" "${rps}" "${capacity_admitted}" "${capacity_p95}" "${error_pct}" "${peak}" "${capacity_limited}" "${capacity_overloaded}" "${capacity_other}" "${capacity_met}"
  printf 'engine=%s phase=overload concurrency=%s requests=%s admitted=%s admitted_p95_ms=%s status_429=%s status_503=%s invalid_retry_after=%s other=%s target_met=%s\n' \
    "${engine}" "${overload_concurrency}" "${overload_total}" "${overload_admitted}" "${overload_p95}" "${overload_limited}" "${overload_overloaded}" "${overload_invalid_retry}" "${overload_other}" "${overload_met}"
  if [[ "${capacity_met}" != yes || "${overload_met}" != yes ]]; then
    targets_failed=1
  fi
}

run_engine() {
  local engine="$1" driver="$2" dsn="$3" port="$4" pid sampler_pid worker_requests
  local base_url="http://127.0.0.1:${port}" peak_file="${workdir}/${engine}.peak-rss"
  local capacity_file="${workdir}/${engine}.capacity" overload_file="${workdir}/${engine}.overload"
  export BLOOM_SECRET_KEY="${secret}" BLOOM_DB_DRIVER="${driver}" BLOOM_DB_DSN="${dsn}"
  export BLOOM_HTTP_ADDR="127.0.0.1:${port}" BLOOM_SESSION_COOKIE_SECURE=false
  export BLOOM_TRUSTED_PROXY_CIDRS="127.0.0.0/8" BLOOM_BOOTSTRAP_PASSWORD="${password}"
  unset BLOOM_LOGIN_RATE_BURST BLOOM_LOGIN_RATE_REFILL_INTERVAL BLOOM_LOGIN_RATE_MAX_KEYS BLOOM_LOGIN_MAX_CONCURRENT
  "${binary}" -migrate >/dev/null
  "${binary}" create-admin --username "${username}" >/dev/null 2>/dev/null
  "${binary}" >"${workdir}/${engine}.log" 2>"${workdir}/${engine}.audit" &
  pid=$!
  pids+=("${pid}")
  wait_ready "${base_url}" "${pid}"
  echo 0 >"${peak_file}"
  sample_peak_rss "${pid}" "${peak_file}" &
  sampler_pid=$!
  pids+=("${sampler_pid}")
  worker_requests=$(((duration * target_rps * 2 + capacity_concurrency - 1) / capacity_concurrency))
  run_phase capacity "${base_url}" "${capacity_concurrency}" "${worker_requests}" "${capacity_file}"
  run_phase overload "${base_url}" "${overload_concurrency}" 4 "${overload_file}"
  kill "${sampler_pid}" 2>/dev/null || true
  wait "${sampler_pid}" 2>/dev/null || true
  report_engine "${engine}" "${capacity_file}" "${overload_file}" "${peak_file}"
  kill "${pid}"
  wait "${pid}" || true
}

require_positive_integer BLOOM_LOAD_DURATION_SECONDS "${duration}"
require_positive_integer BLOOM_LOAD_CONCURRENCY "${capacity_concurrency}"
require_positive_integer BLOOM_LOAD_OVERLOAD_CONCURRENCY "${overload_concurrency}"
if (( overload_concurrency < 20 )); then
  echo "BLOOM_LOAD_OVERLOAD_CONCURRENCY must be at least 20 to exceed default admission and burst limits" >&2
  exit 2
fi
echo "capacity_target: concurrency=${capacity_concurrency} rps>=${target_rps} admitted_p95_ms<=${target_p95_ms} error_rate_pct<=${target_error_pct} peak_rss_bytes<=${target_rss_bytes}"
echo "overload_target: concurrency=${overload_concurrency} status_429>0 status_503>0 valid_retry_after=yes admitted_p95_ms<=${target_p95_ms}"
(cd "${repo_root}" && go build -o "${binary}" ./cmd/bloom)
run_engine sqlite sqlite "file:${workdir}/sqlite.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)" 18080
if [[ -n "${postgres_dsn}" ]]; then
  run_engine postgres postgres "${postgres_dsn}" 18081
else
  echo "postgres skipped: set BLOOM_LOAD_POSTGRES_DSN to include it"
fi
exit "${targets_failed}"
