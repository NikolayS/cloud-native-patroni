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

function kube() {
  kubectl --context "${KUBE_CONTEXT}" --namespace "${NAMESPACE}" "$@"
}

function context_guard() {
  local resolved_context

  if ! resolved_context="$(kube config view --minify --output 'jsonpath={.current-context}' 2>/dev/null)"; then
    printf 'Context guard failed: context %s does not exist.\n' "${KUBE_CONTEXT}" >&2
    return 1
  fi
  if [[ "${resolved_context}" != kind-* || "${resolved_context}" != "${KUBE_CONTEXT}" ]]; then
    printf 'Context guard failed: resolved context %s is not the expected kind context %s.\n' \
      "${resolved_context}" "${KUBE_CONTEXT}" >&2
    return 1
  fi
}

function cluster_exists() {
  kind get clusters 2>/dev/null | awk -v cluster="${CLUSTER_NAME}" '$0 == cluster { found = 1 } END { exit found ? 0 : 1 }'
}

function main() {
  command -v kind >/dev/null 2>&1 || {
    printf 'kind is required. Install kind v0.32.0 and retry.\n' >&2
    return 1
  }
  command -v kubectl >/dev/null 2>&1 || {
    printf 'kubectl is required for the context safety guard. Install kubectl v1.34.1 and retry.\n' >&2
    return 1
  }
  context_guard
  if ! cluster_exists; then
    printf 'Kind cluster %s does not exist; nothing to delete.\n' "${CLUSTER_NAME}"
    return
  fi
  kind delete cluster --name "${CLUSTER_NAME}"
}

main "$@"
