#!/usr/bin/env bash
##
## Copyright © contributors to CloudNativePG, established as
## CloudNativePG a Series of LF Projects, LLC.
##
## Licensed under the Apache License, Version 2.0 (the "License");
## you may not use this file except in compliance with the License.
## You may obtain a copy of the License at
##
##     http://www.apache.org/licenses/LICENSE-2.0
##
## Unless required by applicable law or agreed to in writing, software
## distributed under the License is distributed on an "AS IS" BASIS,
## WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
## See the License for the specific language governing permissions and
## limitations under the License.
##
## SPDX-License-Identifier: Apache-2.0

set -Eeuo pipefail
IFS=$'\n\t'

# These runtime contracts use the production image plus two pure-Python packages, procps, and a
# DCS section swap. The entrypoint, Patroni version, Postgres binaries, user, ownership, and process
# topology are identical. The Kubernetes DCS, selectorless write Service, and Endpoints object are
# proven by the manifests track on kind, not by this bare-container harness.

TEST_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly TEST_DIR
IMAGE_DIR="$(cd -- "${TEST_DIR}/.." && pwd)"
readonly IMAGE_DIR
readonly RUN_ID="${$}"
readonly PROD_IMAGE="cnpatroni-postgres:18.4-patroni-4.1.4-contract-${RUN_ID}"
readonly TEST_IMAGE="cnpatroni-postgres-test:18.4-patroni-4.1.4-contract-${RUN_ID}"
readonly PLATFORM="${CNPATRONI_PLATFORM:-linux/arm64}"
readonly TEST_SCOPE="cnpatroni-contract-rw"

declare -a CREATED_CONTAINERS=()
declare -a CREATED_VOLUMES=()
declare -a CREATED_IMAGES=()
declare -a CREATED_DIRECTORIES=()
declare -i CONTAINER_SEQUENCE=0
declare -i CURRENT_TEST_ASSERTION_FAILURES=0
declare -i FAILURES=0
STARTED_CONTAINER=""

cleanup() {
  local container
  local directory
  local image
  local volume

  for container in "${CREATED_CONTAINERS[@]}"; do
    docker rm -f -v "${container}" >/dev/null 2>&1 || true
  done
  for volume in "${CREATED_VOLUMES[@]}"; do
    docker volume rm -f "${volume}" >/dev/null 2>&1 || true
  done
  for directory in "${CREATED_DIRECTORIES[@]}"; do
    rm -rf -- "${directory}"
  done
  for image in "${CREATED_IMAGES[@]}"; do
    docker image rm -f "${image}" >/dev/null 2>&1 || true
  done
}

fail() {
  printf '%s\n' "$*" >&2
  ((CURRENT_TEST_ASSERTION_FAILURES += 1))
  return 1
}

wait_until_stopped() {
  local container="$1"
  local timeout_seconds="$2"
  local deadline=$((SECONDS + timeout_seconds))

  while ((SECONDS < deadline)); do
    if [[ "$(docker inspect --format '{{.State.Running}}' "${container}")" == "false" ]]; then
      return 0
    fi
    sleep 1
  done

  fail "container ${container} was still running after ${timeout_seconds} seconds"
}

wait_for_leader() {
  local container="$1"
  local timeout_seconds="$2"
  local deadline=$((SECONDS + timeout_seconds))

  while ((SECONDS < deadline)); do
    if docker logs "${container}" 2>&1 | grep -Fq 'the leader with the lock'; then
      return 0
    fi
    if [[ "$(docker inspect --format '{{.State.Running}}' "${container}")" == "false" ]]; then
      docker logs "${container}" >&2 || true
      fail "container ${container} exited before becoming leader"
      return 1
    fi
    sleep 1
  done

  docker logs "${container}" >&2 || true
  fail "container ${container} did not become leader within ${timeout_seconds} seconds"
}

start_test_container() {
  local label="$1"
  local container
  local secret_volume

  ((CONTAINER_SEQUENCE += 1))
  container="cnpatroni-${label}-${RUN_ID}-${CONTAINER_SEQUENCE}"
  secret_volume="${container}-secrets"
  docker volume create "${secret_volume}" >/dev/null
  CREATED_VOLUMES+=("${secret_volume}")
  docker run --rm \
    --entrypoint bash \
    --volume "${secret_volume}:/etc/cnpatroni/secrets" \
    "${TEST_IMAGE}" \
    -c 'umask 077; printf "%s\n" "contract-app-password" > /etc/cnpatroni/secrets/app-password'
  docker run --detach \
    --name "${container}" \
    --volume "${secret_volume}:/etc/cnpatroni/secrets:ro" \
    "${TEST_IMAGE}" >/dev/null
  CREATED_CONTAINERS+=("${container}")
  STARTED_CONTAINER="${container}"
  wait_for_leader "${container}" 120
}

test_function_passes() {
  local test_function="$1"
  local test_status

  CURRENT_TEST_ASSERTION_FAILURES=0
  if "${test_function}"; then
    test_status=0
  else
    test_status=$?
  fi

  ((CURRENT_TEST_ASSERTION_FAILURES == 0 && test_status == 0))
}

run_test() {
  local name="$1"
  local test_function="$2"

  if test_function_passes "${test_function}"; then
    printf 'PASS %s\n' "${name}"
  else
    printf 'FAIL %s\n' "${name}"
    ((FAILURES += 1))
  fi
}

harness_fixture_first_assertion_violated() {
  false || fail "first fixture assertion was intentionally violated"
  true || fail "last fixture assertion unexpectedly failed"
}

harness_fixture_last_assertion_violated() {
  true || fail "first fixture assertion unexpectedly failed"
  false || fail "last fixture assertion was intentionally violated"
}

harness_fixture_no_assertion_violated() {
  true || fail "first fixture assertion unexpectedly failed"
  true || fail "last fixture assertion unexpectedly failed"
}

test_harness_enforces_every_assertion() {
  local -i failures_before="${FAILURES}"
  local -i first_fixture_failed=0
  local -i last_fixture_failed=0
  local -i passing_fixture_passed=0
  local -i self_test_failed=0

  run_test "first-assertion-violated-fixture" \
    harness_fixture_first_assertion_violated >/dev/null 2>&1
  if ((FAILURES == failures_before + 1)); then
    first_fixture_failed=1
  fi
  FAILURES="${failures_before}"

  run_test "last-assertion-violated-fixture" \
    harness_fixture_last_assertion_violated >/dev/null 2>&1
  if ((FAILURES == failures_before + 1)); then
    last_fixture_failed=1
  fi
  FAILURES="${failures_before}"

  run_test "no-assertion-violated-fixture" \
    harness_fixture_no_assertion_violated >/dev/null 2>&1
  if ((FAILURES == failures_before)); then
    passing_fixture_passed=1
  fi
  FAILURES="${failures_before}"

  CURRENT_TEST_ASSERTION_FAILURES=0
  if ((first_fixture_failed != 1)); then
    fail "harness passed a fixture whose first assertion failed"
    self_test_failed=1
  fi
  if ((last_fixture_failed != 1)); then
    fail "harness passed a fixture whose last assertion failed"
    self_test_failed=1
  fi
  if ((passing_fixture_passed != 1)); then
    fail "harness failed a fixture with no violated assertions"
    self_test_failed=1
  fi

  return "${self_test_failed}"
}

test_entrypoint_ends_with_exec_patroni() {
  local last_line
  local last_user

  last_line="$(awk 'NF && $1 !~ /^#/ {line=$0} END {print line}' "${IMAGE_DIR}/entrypoint.sh")"
  [[ "${last_line}" =~ ^exec\ patroni\ \"\$CNPATRONI_CONFIG_FILE\"$ ]] ||
    fail "entrypoint last instruction was: ${last_line}"
  if grep -En 'gosu|su-exec|setpriv|tini|dumb-init|supervisord|(^|[^[:alnum:]_])while([^[:alnum:]_]|$)|(^|[^[:alnum:]_])until([^[:alnum:]_]|$)|(^|[^[:alnum:]_])trap([^[:alnum:]_]|$)|&[[:space:]]*$' "${IMAGE_DIR}/entrypoint.sh"; then
    fail "entrypoint contains a forbidden wrapper or control construct"
  fi
  grep -Eq '^STOPSIGNAL[[:space:]]+SIGTERM$' "${IMAGE_DIR}/Dockerfile" ||
    fail "Dockerfile does not declare STOPSIGNAL SIGTERM"
  grep -Eq '^FROM[[:space:]]+postgres@sha256:[[:xdigit:]]+$' "${IMAGE_DIR}/Dockerfile" ||
    fail "Dockerfile base is not pinned by digest"
  last_user="$(awk 'toupper($1) == "USER" {line=$0} END {print line}' "${IMAGE_DIR}/Dockerfile")"
  [[ "${last_user}" == "USER 999:999" ]] || fail "final Dockerfile user was: ${last_user}"
}

test_patroni_is_pid_1() {
  local c
  local cmdline
  local comm
  local forbidden

  start_test_container "pid1"
  c="${STARTED_CONTAINER}"
  comm="$(docker exec "${c}" cat /proc/1/comm)"
  # comm is the executable basename until Patroni calls setproctitle, so either value is correct;
  # PID 1 identity is asserted unambiguously by cmdline below.
  [[ "${comm}" == "patroni" || "${comm}" == "python3" ]] || fail "PID 1 comm was ${comm}"
  cmdline="$(docker exec "${c}" sh -c "tr '\\0' ' ' < /proc/1/cmdline")"
  [[ "${cmdline}" == *"/etc/cnpatroni/patroni.yml " ]] ||
    fail "PID 1 command line did not end with the test configuration path: ${cmdline}"
  forbidden="$(docker exec "${c}" ps -eo comm= | awk '$1 ~ /^(tini|dumb-init|gosu|supervisord|bash|sh|dash)$/ {print}')"
  [[ -z "${forbidden}" ]] || fail "found forbidden supervisor or bare shell: ${forbidden}"
}

test_pid_1_catches_sigterm() {
  local c
  local mask_hex
  local mask_value
  local signal_number

  start_test_container "signals"
  c="${STARTED_CONTAINER}"
  mask_hex="$(docker exec "${c}" awk '/^SigCgt:/ {print $2}' /proc/1/status)"
  mask_value=$((16#${mask_hex}))
  for signal_number in 15 17 2 1; do
    if (( (mask_value & (1 << (signal_number - 1))) == 0 )); then
      fail "SigCgt ${mask_hex} did not include signal ${signal_number}"
      return 1
    fi
  done
}

test_sigterm_shuts_postgres_down_gracefully() {
  local c
  local exit_code
  local logs

  start_test_container "sigterm"
  c="${STARTED_CONTAINER}"
  docker kill --signal TERM "${c}" >/dev/null
  wait_until_stopped "${c}" 30
  exit_code="$(docker inspect --format '{{.State.ExitCode}}' "${c}")"
  [[ "${exit_code}" == "0" ]] || fail "container exit code was ${exit_code}"
  logs="$(docker logs "${c}" 2>&1)"
  grep -Fq 'received fast shutdown request' <<<"${logs}" ||
    fail "logs did not contain the fast-shutdown message"
  grep -Fq 'database system is shut down' <<<"${logs}" ||
    fail "logs did not contain the completed-shutdown message"
}

test_container_exits_when_patroni_exits() {
  local c
  local child_pid
  local lock_lines_after
  local lock_lines_before

  start_test_container "child-exit"
  c="${STARTED_CONTAINER}"
  child_pid="$(docker exec "${c}" ps -eo pid=,ppid=,args= | awk '$2 == 1 && $1 != 1 && /patroni/ {print $1; exit}')"
  [[ -n "${child_pid}" ]] || fail "could not identify the Patroni child"
  lock_lines_before="$(docker logs "${c}" 2>&1 | grep -Fc 'Lock owner:' || true)"
  ((lock_lines_before > 0)) || fail "logs contained no Patroni HA-loop startup marker"
  docker exec "${c}" kill -9 "${child_pid}"
  wait_until_stopped "${c}" 30
  lock_lines_after="$(docker logs "${c}" 2>&1 | grep -Fc 'Lock owner:' || true)"
  [[ "${lock_lines_after}" == "${lock_lines_before}" ]] ||
    fail "Patroni emitted a second HA-loop startup marker after its child was killed"
}

test_no_postgres_survives_container_exit() {
  local c
  local deadline
  local host_pids
  local sweep_output

  start_test_container "namespace-exit"
  c="${STARTED_CONTAINER}"
  host_pids="$(docker top "${c}" | awk 'NR > 1 {print $2}')"
  [[ -n "${host_pids}" ]] || fail "docker top returned no host PIDs"
  docker kill --signal KILL "${c}" >/dev/null
  wait_until_stopped "${c}" 30
  deadline=$((SECONDS + 30))
  while true; do
    if sweep_output="$(docker run --rm \
      --pid host \
      --entrypoint bash \
      --env "CNPATRONI_RECORDED_PIDS=${host_pids}" \
      --env "CNPATRONI_TEST_SCOPE=${TEST_SCOPE}" \
      "${TEST_IMAGE}" \
      -c '
        set -Eeuo pipefail
        sweep_failed=0
        while IFS= read -r recorded_pid; do
          if [[ -n "${recorded_pid}" && -e "/proc/${recorded_pid}" ]]; then
            printf "recorded host PID %s survived\n" "${recorded_pid}" >&2
            sweep_failed=1
          fi
        done <<<"${CNPATRONI_RECORDED_PIDS}"
        if pgrep -af "cluster_name=${CNPATRONI_TEST_SCOPE}"; then
          printf "a process matching the test cluster survived\n" >&2
          sweep_failed=1
        fi
        exit "${sweep_failed}"
      ' 2>&1)"; then
      return 0
    fi
    if ((SECONDS >= deadline)); then
      break
    fi
    sleep 1
  done

  fail "host PID sweep failed after 30 seconds: ${sweep_output}"
}

test_postmaster_kill_leaves_no_zombies() {
  local c
  local deadline
  local new_postmaster_pid
  local old_postmaster_pid
  local recovered=0
  local sleeper_count
  local zombie_count

  start_test_container "postmaster-kill"
  c="${STARTED_CONTAINER}"
  for _ in 1 2 3; do
    docker exec --detach "${c}" psql --no-psqlrc --command 'SELECT pg_sleep(120)' >/dev/null
  done
  deadline=$((SECONDS + 30))
  sleeper_count=0
  while ((SECONDS < deadline)); do
    sleeper_count="$(docker exec "${c}" psql --no-psqlrc --tuples-only --no-align --command \
      "SELECT count(*) FROM pg_stat_activity WHERE query = 'SELECT pg_sleep(120)'")"
    if ((sleeper_count >= 3)); then
      break
    fi
    sleep 1
  done
  ((sleeper_count >= 3)) || fail "three extra Postgres backends did not connect"
  old_postmaster_pid="$(docker exec "${c}" head -n 1 /var/lib/postgresql/pgdata/postmaster.pid)"
  docker exec "${c}" kill -9 "${old_postmaster_pid}"

  # Patroni restarting Postgres here is its own high-availability decision and is permitted.
  deadline=$((SECONDS + 30))
  while ((SECONDS < deadline)); do
    zombie_count="$(docker exec "${c}" ps -eo stat= | awk '$1 ~ /^Z/ {count++} END {print count+0}')"
    ((zombie_count == 0)) || fail "found ${zombie_count} zombie processes after the postmaster kill"
    if new_postmaster_pid="$(docker exec "${c}" head -n 1 /var/lib/postgresql/pgdata/postmaster.pid 2>/dev/null)" &&
      [[ "${new_postmaster_pid}" != "${old_postmaster_pid}" ]] &&
      docker exec "${c}" psql --no-psqlrc --tuples-only --no-align --command 'SELECT 1' >/dev/null 2>&1; then
      recovered=1
    fi
    sleep 1
  done
  ((recovered == 1)) || fail "Patroni did not bring Postgres back within 30 seconds"
}

write_renderer_secrets() {
  local directory="$1"

  printf '%s\n' 'render-superuser-password' >"${directory}/superuser-password"
  printf '%s\n' 'render-replication-password' >"${directory}/replication-password"
  printf '%s\n' 'render-rewind-password' >"${directory}/rewind-password"
  printf '%s\n' 'render-restapi-password' >"${directory}/restapi-password"
  printf '%s\n' 'render-app-password' >"${directory}/app-password"
  chmod 0755 "${directory}"
  chmod 0644 "${directory}"/*
}

renderer_docker_args() {
  printf '%s\n' \
    --env CNPATRONI_SCOPE=render-contract-rw \
    --env CNPATRONI_NAME=render-contract-0 \
    --env CNPATRONI_NAMESPACE=default \
    --env CNPATRONI_POD_IP=10.244.0.10 \
    --env CNPATRONI_SECRETS_DIR=/run/cnpatroni-secrets \
    --volume "${1}:/run/cnpatroni-secrets:ro"
}

test_rendered_config_is_valid_and_0600() {
  local collision_output
  local missing_output
  local output
  local secret
  local secrets_directory
  local -a docker_args=()

  secrets_directory="$(mktemp -d "${IMAGE_DIR}/.contract-secrets.XXXXXX")"
  CREATED_DIRECTORIES+=("${secrets_directory}")
  write_renderer_secrets "${secrets_directory}"
  while IFS= read -r argument; do
    docker_args+=("${argument}")
  done < <(renderer_docker_args "${secrets_directory}")

  if ! output="$(docker run --rm \
    --entrypoint bash \
    "${docker_args[@]}" \
    "${PROD_IMAGE}" \
    -c 'cnpatroni-render-config && stat -c "mode=%a" "${CNPATRONI_CONFIG_FILE}" && patroni --validate-config "${CNPATRONI_CONFIG_FILE}"' 2>&1)"; then
    fail "renderer or Patroni validation failed: ${output}"
    return 1
  fi
  grep -Fq 'mode=600' <<<"${output}" || fail "rendered configuration mode was not 0600: ${output}"
  for secret in "${secrets_directory}"/*; do
    if grep -Fq "$(<"${secret}")" <<<"${output}"; then
      fail "renderer output exposed a password from $(basename -- "${secret}")"
      return 1
    fi
  done

  if missing_output="$(docker run --rm \
    --entrypoint cnpatroni-render-config \
    "${docker_args[@]}" \
    --env CNPATRONI_SCOPE= \
    "${PROD_IMAGE}" 2>&1)"; then
    fail "renderer accepted an empty required variable"
  fi
  grep -Fq 'CNPATRONI_SCOPE' <<<"${missing_output}" ||
    fail "missing-variable error did not name CNPATRONI_SCOPE"

  cp "${secrets_directory}/superuser-password" "${secrets_directory}/app-password"
  if collision_output="$(docker run --rm \
    --entrypoint cnpatroni-render-config \
    "${docker_args[@]}" \
    "${PROD_IMAGE}" 2>&1)"; then
    fail "renderer accepted colliding credentials"
  fi
  grep -Fq 'superuser and app' <<<"${collision_output}" ||
    fail "credential-collision error did not name the colliding roles"
  if grep -Fq 'render-superuser-password' <<<"${collision_output}"; then
    fail "credential-collision error exposed the colliding password"
  fi
}

build_images() {
  CNPATRONI_IMAGE="${PROD_IMAGE}" CNPATRONI_PLATFORM="${PLATFORM}" "${IMAGE_DIR}/build.sh"
  CREATED_IMAGES+=("${PROD_IMAGE}")
  docker build \
    --platform "${PLATFORM}" \
    --build-arg "CNPATRONI_BASE_IMAGE=${PROD_IMAGE}" \
    --tag "${TEST_IMAGE}" \
    --file "${TEST_DIR}/Dockerfile.test" \
    "${TEST_DIR}"
  CREATED_IMAGES+=("${TEST_IMAGE}")
}

main() {
  trap cleanup EXIT
  build_images
  run_test "harness-enforces-every-assertion" test_harness_enforces_every_assertion
  run_test "entrypoint-ends-with-exec-patroni" test_entrypoint_ends_with_exec_patroni
  run_test "patroni-is-pid-1" test_patroni_is_pid_1
  run_test "pid-1-catches-sigterm" test_pid_1_catches_sigterm
  run_test "sigterm-shuts-postgres-down-gracefully" test_sigterm_shuts_postgres_down_gracefully
  run_test "container-exits-when-patroni-exits" test_container_exits_when_patroni_exits
  run_test "no-postgres-survives-container-exit" test_no_postgres_survives_container_exit
  run_test "postmaster-kill-leaves-no-zombies" test_postmaster_kill_leaves_no_zombies
  run_test "rendered-config-is-valid-and-0600" test_rendered_config_is_valid_and_0600
  ((FAILURES == 0))
}

main "$@"
