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

readonly CLUSTER_NAME='cnpatroni-poc'
readonly KUBE_CONTEXT="kind-${CLUSTER_NAME}"
readonly NAMESPACE='cnpatroni-poc'
readonly SECRET_NAME='cnpatroni-poc-credentials'
readonly IMAGE='cnpatroni/patroni-postgres:18-4.1.4-poc1'
readonly INSTANCE_SELECTOR='cnpatroni.io/podRole=instance'
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly MANIFEST_DIR="${SCRIPT_DIR}/../manifests"

CREDENTIAL_DIRECTORY=''

function kube() {
  kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" "$@"
}

function context_guard() {
  local resolved_context

  if ! resolved_context="$(kube config view --minify --output 'jsonpath={.current-context}' 2>/dev/null)"; then
    printf 'Context guard failed: context %s was not created by kind.\n' "${KUBE_CONTEXT}" >&2
    return 1
  fi
  if [[ "${resolved_context}" != kind-* || "${resolved_context}" != "${KUBE_CONTEXT}" ]]; then
    printf 'Context guard failed: resolved context %s is not the expected kind context %s.\n' \
      "${resolved_context}" "${KUBE_CONTEXT}" >&2
    return 1
  fi
}

function require_command() {
  local command_name="$1"
  local fix_message="$2"

  if ! command -v "${command_name}" >/dev/null 2>&1; then
    printf '%s is required. %s\n' "${command_name}" "${fix_message}" >&2
    return 1
  fi
}

function preflight() {
  local docker_context

  require_command docker 'Install Docker and select a working Docker context.'
  require_command kind 'Install kind v0.32.0 and place it on PATH.'
  require_command kubectl 'Install kubectl v1.34.1 and place it on PATH.'
  require_command openssl 'Install OpenSSL and place openssl on PATH.'

  if ! docker info >/dev/null 2>&1; then
    docker_context="$(docker context show 2>/dev/null || printf '<unknown>')"
    printf 'Docker daemon is unreachable through context %s. Start that daemon or select a working context manually.\n' \
      "${docker_context}" >&2
    printf 'Available Docker contexts:\n' >&2
    docker context ls >&2 || true
    return 1
  fi

  if ! docker image inspect "${IMAGE}" >/dev/null 2>&1; then
    printf 'Database image %s is missing. Build it in the image track before running bring-up.\n' \
      "${IMAGE}" >&2
    return 1
  fi
}

function cluster_exists() {
  kind get clusters 2>/dev/null | awk -v cluster="${CLUSTER_NAME}" '$0 == cluster { found = 1 } END { exit found ? 0 : 1 }'
}

function ensure_cluster() {
  if cluster_exists; then
    printf 'Kind cluster %s already exists.\n' "${CLUSTER_NAME}"
  else
    printf 'Creating kind cluster %s.\n' "${CLUSTER_NAME}"
    kind create cluster --config "${MANIFEST_DIR}/kind/kind-cluster.yaml" --wait 120s
  fi
}

function cleanup_credentials() {
  if [[ -n "${CREDENTIAL_DIRECTORY}" && -d "${CREDENTIAL_DIRECTORY}" ]]; then
    rm -rf -- "${CREDENTIAL_DIRECTORY}"
  fi
  CREDENTIAL_DIRECTORY=''
}

function generate_password_files() {
  local unique_count

  while true; do
    openssl rand -hex 24 | tr -d '\n' > "${CREDENTIAL_DIRECTORY}/superuser-password"
    openssl rand -hex 24 | tr -d '\n' > "${CREDENTIAL_DIRECTORY}/replication-password"
    openssl rand -hex 24 | tr -d '\n' > "${CREDENTIAL_DIRECTORY}/rewind-password"
    openssl rand -hex 24 | tr -d '\n' > "${CREDENTIAL_DIRECTORY}/app-password"
    openssl rand -hex 24 | tr -d '\n' > "${CREDENTIAL_DIRECTORY}/restapi-password"
    unique_count="$(
      sort -u \
        "${CREDENTIAL_DIRECTORY}/superuser-password" \
        "${CREDENTIAL_DIRECTORY}/replication-password" \
        "${CREDENTIAL_DIRECTORY}/rewind-password" \
        "${CREDENTIAL_DIRECTORY}/app-password" \
        "${CREDENTIAL_DIRECTORY}/restapi-password" | wc -l | tr -d '[:space:]'
    )"
    [[ "${unique_count}" == '5' ]] && return
  done
}

function create_secret_if_absent() {
  local existing_secret

  if ! existing_secret="$(
    kube get secret "${SECRET_NAME}" --ignore-not-found --output name
  )"; then
    printf 'Unable to check whether Secret %s exists.\n' "${SECRET_NAME}" >&2
    return 1
  fi
  if [[ -n "${existing_secret}" ]]; then
    printf 'Secret %s already exists; leaving credentials unchanged.\n' "${SECRET_NAME}"
    return
  fi

  umask 077
  CREDENTIAL_DIRECTORY="$(mktemp -d "${TMPDIR:-/tmp}/cnpatroni-poc-credentials.XXXXXX")"
  generate_password_files
  printf '%s' 'postgres' > "${CREDENTIAL_DIRECTORY}/superuser-username"
  printf '%s' 'cnpatroni_replication' > "${CREDENTIAL_DIRECTORY}/replication-username"
  printf '%s' 'cnpatroni_rewind' > "${CREDENTIAL_DIRECTORY}/rewind-username"
  printf '%s' 'app' > "${CREDENTIAL_DIRECTORY}/app-username"
  printf '%s' 'app' > "${CREDENTIAL_DIRECTORY}/app-database"
  printf '%s' 'cnpatroni_restapi' > "${CREDENTIAL_DIRECTORY}/restapi-username"

  kube create secret generic "${SECRET_NAME}" \
    --from-file="${CREDENTIAL_DIRECTORY}/superuser-username" \
    --from-file="${CREDENTIAL_DIRECTORY}/superuser-password" \
    --from-file="${CREDENTIAL_DIRECTORY}/replication-username" \
    --from-file="${CREDENTIAL_DIRECTORY}/replication-password" \
    --from-file="${CREDENTIAL_DIRECTORY}/rewind-username" \
    --from-file="${CREDENTIAL_DIRECTORY}/rewind-password" \
    --from-file="${CREDENTIAL_DIRECTORY}/app-username" \
    --from-file="${CREDENTIAL_DIRECTORY}/app-password" \
    --from-file="${CREDENTIAL_DIRECTORY}/app-database" \
    --from-file="${CREDENTIAL_DIRECTORY}/restapi-username" \
    --from-file="${CREDENTIAL_DIRECTORY}/restapi-password"
  cleanup_credentials
}

function apply_manifests() {
  kube apply --filename "${MANIFEST_DIR}/cluster/00-namespace.yaml"
  kube apply --filename "${MANIFEST_DIR}/cluster/10-rbac.yaml"
  kube apply --filename "${MANIFEST_DIR}/cluster/20-config.yaml"
  create_secret_if_absent
  kube apply --filename "${MANIFEST_DIR}/cluster/30-services.yaml"
  kube apply --filename "${MANIFEST_DIR}/cluster/40-storage.yaml"
  kube apply --filename "${MANIFEST_DIR}/cluster/50-instances.yaml"
}

function dump_readiness_diagnostics() {
  local pod_refs pod_ref ready_status

  printf 'Instance readiness timed out. Namespace diagnostics follow.\n' >&2
  kube get pods --output wide >&2 || true
  kube get endpoints >&2 || true
  pod_refs="$(kube get pods --selector "${INSTANCE_SELECTOR}" --output name 2>/dev/null || true)"
  while IFS= read -r pod_ref; do
    [[ -n "${pod_ref}" ]] || continue
    ready_status="$(
      kube get "${pod_ref}" --output 'jsonpath={.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || true
    )"
    if [[ "${ready_status}" != 'True' ]]; then
      kube describe "${pod_ref}" >&2 || true
    fi
    printf 'Last 100 Patroni log lines for %s:\n' "${pod_ref#pod/}" >&2
    kube logs "${pod_ref}" --container patroni --tail=100 >&2 || true
  done <<< "${pod_refs}"
}

function wait_for_instances() {
  if ! kube wait --for=condition=Ready pod --selector "${INSTANCE_SELECTOR}" \
    --timeout=600s; then
    dump_readiness_diagnostics
    return 1
  fi
}

function main() {
  trap cleanup_credentials EXIT
  preflight
  ensure_cluster
  context_guard
  kind load docker-image "${IMAGE}" --name "${CLUSTER_NAME}"
  apply_manifests
  wait_for_instances
  "${SCRIPT_DIR}/verify.sh"
}

main "$@"
