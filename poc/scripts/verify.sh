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
##

set -Eeuo pipefail
IFS=$'\n\t'

readonly CLUSTER_NAME="cnpatroni-poc"
readonly KUBE_CONTEXT="kind-${CLUSTER_NAME}"
readonly NAMESPACE="cnpatroni-poc"
readonly INSTANCE_SELECTOR="cnpatroni.io/podRole=instance"
readonly PRIMARY_SELECTOR="${INSTANCE_SELECTOR},cnpatroni.io/role=primary"
readonly REPLICA_SELECTOR="${INSTANCE_SELECTOR},cnpatroni.io/role=replica"
readonly VERIFY_POD="cnpatroni-poc-verify"
readonly IMAGE="cnpatroni/patroni-postgres:18-4.1.4-poc1"
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly MANIFEST_DIR="${SCRIPT_DIR}/../manifests/cluster"

FAILURES=0

function kube() {
  kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" "$@"
}

function context_guard() {
  local resolved_context

  if ! resolved_context="$(kube config view --minify --output 'jsonpath={.current-context}' 2>/dev/null)"; then
    printf 'Context guard failed: context %s does not exist. Run poc/scripts/bringup.sh first.\n' \
      "${KUBE_CONTEXT}" >&2
    return 1
  fi
  if [[ "${resolved_context}" != kind-* || "${resolved_context}" != "${KUBE_CONTEXT}" ]]; then
    printf 'Context guard failed: resolved context %s is not the expected kind context %s.\n' \
      "${resolved_context}" "${KUBE_CONTEXT}" >&2
    return 1
  fi
}

function pass() {
  printf 'Pass: %s\n' "$1"
}

function fail() {
  printf 'Fail: %s\n' "$1" >&2
  FAILURES=$((FAILURES + 1))
}

function nonempty_line_count() {
  awk 'NF { count++ } END { print count + 0 }' <<< "$1"
}

function normalized_lines() {
  awk 'NF' <<< "$1" | sort -u
}

function pod_rows() {
  kube get pods --selector "$1" \
    --output 'jsonpath={range .items[*]}{.metadata.name}{"\t"}{.status.podIP}{"\n"}{end}'
}

function endpoint_addresses() {
  kube get endpoints "$1" \
    --output 'jsonpath={range .subsets[*].addresses[*]}{.ip}{"\n"}{end}'
}

function check_roles() {
  local instance_count primary_count replica_count

  instance_count="$(nonempty_line_count "${INSTANCE_ROWS}")"
  primary_count="$(nonempty_line_count "${PRIMARY_ROWS}")"
  replica_count="$(nonempty_line_count "${REPLICA_ROWS}")"
  if [[ "${instance_count}" == "3" && "${primary_count}" == "1" && "${replica_count}" == "2" ]]; then
    pass 'Exactly one instance is primary and exactly two instances are replicas.'
  else
    fail "Expected three instances with one primary and two replicas; found ${instance_count} instances, ${primary_count} primary, and ${replica_count} replicas."
  fi
}

function check_write_endpoints() {
  local addresses address_count

  if ! addresses="$(endpoint_addresses 'cnpatroni-poc-rw' 2>/dev/null)"; then
    fail 'The cnpatroni-poc-rw Endpoints object does not exist.'
    return
  fi
  address_count="$(nonempty_line_count "${addresses}")"
  if [[ -n "${PRIMARY_IP}" && "${address_count}" == "1" && "${addresses}" == "${PRIMARY_IP}" ]]; then
    pass "The write Endpoints object contains only the primary IP ${PRIMARY_IP}."
  else
    fail "The write Endpoints addresses '${addresses:-<none>}' do not equal the sole primary IP '${PRIMARY_IP:-<none>}'."
  fi
}

function check_write_endpoint_slice() {
  local addresses address_count start_seconds elapsed

  addresses=''
  start_seconds="${SECONDS}"
  while true; do
    if ! addresses="$(
      # EndpointSlice addresses are scalars, so kubectl JSONPath requires the {@} current-node reference.
      kube get endpointslices.discovery.k8s.io \
        --selector 'kubernetes.io/service-name=cnpatroni-poc-rw' \
        --output 'jsonpath={range .items[*].endpoints[*].addresses[*]}{@}{"\n"}{end}' 2>/dev/null
    )"; then
      addresses='<unavailable>'
    fi
    address_count="$(nonempty_line_count "${addresses}")"
    elapsed=$((SECONDS - start_seconds))
    if [[ -n "${PRIMARY_IP}" && "${address_count}" == "1" && "${addresses}" == "${PRIMARY_IP}" ]]; then
      pass "The mirrored write EndpointSlice converged to the sole primary IP ${PRIMARY_IP} in ${elapsed} seconds."
      return
    fi
    if ((elapsed >= 60)); then
      break
    fi
    sleep 1
  done

  fail "The write EndpointSlice addresses '${addresses:-<none>}' did not converge to the sole primary IP '${PRIMARY_IP:-<none>}' within the 60-second deadline."
}

function check_read_endpoints() {
  local addresses address_count expected actual

  if ! addresses="$(endpoint_addresses 'cnpatroni-poc-ro' 2>/dev/null)"; then
    fail 'The cnpatroni-poc-ro Endpoints object does not exist.'
    return
  fi
  address_count="$(nonempty_line_count "${addresses}")"
  expected="$(normalized_lines "${REPLICA_IPS}")"
  actual="$(normalized_lines "${addresses}")"
  if [[ -n "${PRIMARY_IP}" && "${address_count}" == "2" && "${actual}" == "${expected}" ]] && \
    ! awk -v primary="${PRIMARY_IP}" '$0 == primary { found = 1 } END { exit found ? 0 : 1 }' <<< "${actual}"; then
    pass "The read Endpoints object contains only the two replica IPs: ${actual//$'\n'/, }."
  else
    fail "The read Endpoints addresses '${actual:-<none>}' do not equal the replica IPs '${expected:-<none>}' or include the primary."
  fi
}

function check_config_endpoints() {
  local config_annotation

  if ! config_annotation="$(
    kube get endpoints 'cnpatroni-poc-rw-config' \
      --output 'jsonpath={.metadata.annotations.config}' 2>/dev/null
  )"; then
    fail 'The cnpatroni-poc-rw-config Endpoints object does not exist.'
  elif [[ -n "${config_annotation}" ]]; then
    pass 'The configuration Endpoints object carries the config annotation.'
  else
    fail 'The configuration Endpoints object has no config annotation.'
  fi
}

function service_role_references_are_selectors() {
  awk '
    /^[ ]*selector:[ ]*(#.*)?$/ {
      in_selector = 1
      selector_indent = match($0, /[^ ]/) - 1
      next
    }
    {
      if (in_selector && $0 !~ /^[ ]*($|#)/) {
        current_indent = match($0, /[^ ]/) - 1
        if (current_indent <= selector_indent) {
          in_selector = 0
        }
      }
      if (index($0, "cnpatroni.io/role") && !in_selector) {
        invalid = 1
      }
    }
    END { exit invalid ? 1 : 0 }
  ' "${MANIFEST_DIR}/30-services.yaml"
}

function check_static_role_ownership() {
  if grep -Fq 'cnpatroni.io/role' "${MANIFEST_DIR}/50-instances.yaml"; then
    fail 'The instance manifest writes or mentions the Patroni-owned role label.'
  elif ! service_role_references_are_selectors; then
    fail 'The Service manifest references the Patroni-owned role label outside a selector block.'
  else
    pass 'Static manifests leave role-label writes to Patroni and only select on the label.'
  fi
}

function check_pid_one() {
  local pod_refs pod_ref pod_name comm details separator
  local pid_ok=true

  details=''
  separator=''
  if ! pod_refs="$(kube get pods --selector "${INSTANCE_SELECTOR}" --output name 2>/dev/null)"; then
    pid_ok=false
    pod_refs=''
  fi
  if [[ "$(nonempty_line_count "${pod_refs}")" != "3" ]]; then
    pid_ok=false
  fi
  while IFS= read -r pod_ref; do
    [[ -n "${pod_ref}" ]] || continue
    pod_name="${pod_ref#pod/}"
    if ! comm="$(kube exec "${pod_ref}" --container patroni -- cat /proc/1/comm 2>/dev/null)"; then
      comm='<unreadable>'
      pid_ok=false
    fi
    case "${comm}" in
      patroni | python | python[0-9]* ) ;;
      * ) pid_ok=false ;;
    esac
    details+="${separator}${pod_name}=${comm}"
    separator=', '
  done <<< "${pod_refs}"

  if [[ "${pid_ok}" == true ]]; then
    pass "Patroni's PID 1 init shim is present (${details})."
  else
    fail "PID 1 did not identify as Patroni or Python on every instance (${details:-no instances})."
  fi
}

function check_rendered_config_permissions() {
  local pod_refs pod_ref pod_name attributes details separator
  local permissions_ok=true

  details=''
  separator=''
  if ! pod_refs="$(kube get pods --selector "${INSTANCE_SELECTOR}" --output name 2>/dev/null)"; then
    permissions_ok=false
    pod_refs=''
  fi
  if [[ "$(nonempty_line_count "${pod_refs}")" != "3" ]]; then
    permissions_ok=false
  fi
  while IFS= read -r pod_ref; do
    [[ -n "${pod_ref}" ]] || continue
    pod_name="${pod_ref#pod/}"
    if ! attributes="$(
      kube exec "${pod_ref}" --container patroni -- \
        stat -c '%a:%u' /run/cnpatroni/patroni.yml 2>/dev/null
    )"; then
      attributes='<unreadable>'
      permissions_ok=false
    elif [[ "${attributes}" != '600:999' ]]; then
      permissions_ok=false
    fi
    details+="${separator}${pod_name}=${attributes}"
    separator=', '
  done <<< "${pod_refs}"

  if [[ "${permissions_ok}" == true ]]; then
    pass "Rendered Patroni configuration is mode 0600 and owned by uid 999 (${details})."
  else
    fail "Rendered Patroni configuration permissions are wrong (${details:-no instances})."
  fi
}

function cleanup_verify_pod() {
  kube delete pod "${VERIFY_POD}" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}

function check_service_recovery_state() {
  local result

  cleanup_verify_pod
  if ! kube apply --filename - >/dev/null <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${VERIFY_POD}
spec:
  restartPolicy: Never
  containers:
    - name: psql
      image: ${IMAGE}
      imagePullPolicy: Never
      command: ["/bin/bash", "-c"]
      args:
        - |
          set -Eeuo pipefail
          rw="\$(psql --host=cnpatroni-poc-rw --tuples-only --no-align --command='SELECT pg_is_in_recovery()')"
          ro="\$(psql --host=cnpatroni-poc-ro --tuples-only --no-align --command='SELECT pg_is_in_recovery()')"
          printf 'rw=%s\\nro=%s\\n' "\${rw}" "\${ro}"
      env:
        - name: PGPASSWORD
          valueFrom:
            secretKeyRef:
              name: cnpatroni-poc-credentials
              key: app-password
        - name: PGUSER
          valueFrom:
            secretKeyRef:
              name: cnpatroni-poc-credentials
              key: app-username
        - name: PGDATABASE
          valueFrom:
            secretKeyRef:
              name: cnpatroni-poc-credentials
              key: app-database
        - name: PGCONNECT_TIMEOUT
          value: "10"
EOF
  then
    fail 'The in-cluster SQL verification Pod could not be created.'
    return
  fi

  if ! kube wait --for="jsonpath={.status.phase}=Succeeded" pod/"${VERIFY_POD}" \
    --timeout=120s >/dev/null 2>&1; then
    result="$(kube logs "${VERIFY_POD}" --container psql 2>&1 || true)"
    fail "The in-cluster SQL verification Pod did not succeed: ${result:-no logs}."
    return
  fi
  result="$(kube logs "${VERIFY_POD}" --container psql 2>/dev/null || true)"
  if [[ "${result}" == $'rw=f\nro=t' ]]; then
    pass 'SQL through the write Service reports recovery=false and through the read Service reports recovery=true.'
  else
    fail "Unexpected SQL recovery states: ${result:-no output}."
  fi
}

function main() {
  local primary_ip_field

  command -v kubectl >/dev/null 2>&1 || {
    printf 'kubectl is required. Install kubectl v1.34.1 and retry.\n' >&2
    return 1
  }
  context_guard
  trap cleanup_verify_pod EXIT

  INSTANCE_ROWS="$(pod_rows "${INSTANCE_SELECTOR}" 2>/dev/null || true)"
  PRIMARY_ROWS="$(pod_rows "${PRIMARY_SELECTOR}" 2>/dev/null || true)"
  REPLICA_ROWS="$(pod_rows "${REPLICA_SELECTOR}" 2>/dev/null || true)"
  primary_ip_field="$(awk 'NF { print $2 }' <<< "${PRIMARY_ROWS}")"
  PRIMARY_IP="${primary_ip_field}"
  REPLICA_IPS="$(awk 'NF { print $2 }' <<< "${REPLICA_ROWS}")"

  check_roles
  check_write_endpoints
  check_write_endpoint_slice
  check_read_endpoints
  check_config_endpoints
  check_static_role_ownership
  check_pid_one
  check_rendered_config_permissions
  check_service_recovery_state

  if ((FAILURES > 0)); then
    printf 'Verification failed with %d failed check(s).\n' "${FAILURES}" >&2
    return 1
  fi
  printf 'Verification passed all nine checks.\n'
}

main "$@"
