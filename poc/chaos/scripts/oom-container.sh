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

memory_max_path=""
original_max=""

restore_limit() {
  if [[ -n "${memory_max_path}" && -n "${original_max}" && -e "${memory_max_path}" ]]; then
    printf '%s\n' "${original_max}" >"${memory_max_path}"
  fi
}

main() {
  if [[ "$#" -ne 1 ]]; then
    echo "usage: oom-container.sh <container-id>" >&2
    return 2
  fi
  if [[ ! -e /sys/fs/cgroup/cgroup.controllers ]]; then
    echo "cgroup v2 is required" >&2
    return 1
  fi

  local host_pid
  host_pid=$(crictl inspect --output go-template --template '{{.info.pid}}' "$1")
  if [[ ! "${host_pid}" =~ ^[0-9]+$ ]] || [[ "${host_pid}" -le 1 ]]; then
    echo "invalid container host PID: ${host_pid}" >&2
    return 1
  fi

  local cgroup_path
  cgroup_path=$(awk -F: '$1 == "0" {print $3}' "/proc/${host_pid}/cgroup")
  local cgroup_root="/sys/fs/cgroup${cgroup_path}"
  memory_max_path="${cgroup_root}/memory.max"
  local memory_current_path="${cgroup_root}/memory.current"
  local memory_events_path="${cgroup_root}/memory.events"
  if [[ ! -w "${memory_max_path}" || ! -r "${memory_current_path}" ]]; then
    echo "container memory controller is unavailable at ${cgroup_root}" >&2
    return 1
  fi

  original_max=$(<"${memory_max_path}")
  local current
  current=$(<"${memory_current_path}")
  local target=$((current > 1048576 ? current - 1048576 : 1))
  local before_oom_kill
  before_oom_kill=$(awk '$1 == "oom_kill" {print $2}' "${memory_events_path}")
  trap restore_limit EXIT
  printf 'host_pid=%s\ncgroup=%s\nmemory.current=%s\noriginal.memory.max=%s\ntarget.memory.max=%s\n' \
    "${host_pid}" "${cgroup_path}" "${current}" "${original_max}" "${target}"
  printf '%s\n' "${target}" >"${memory_max_path}"

  local iteration=1
  while [[ "${iteration}" -le 100 ]]; do
    local after_oom_kill
    after_oom_kill=$(awk '$1 == "oom_kill" {print $2}' "${memory_events_path}")
    if [[ "${after_oom_kill}" -gt "${before_oom_kill}" ]]; then
      printf 'oom_kill.before=%s\noom_kill.after=%s\n' "${before_oom_kill}" "${after_oom_kill}"
      return 0
    fi
    sleep 0.1
    iteration=$((iteration + 1))
  done
  echo "memory limit was applied but no cgroup OOM kill was observed" >&2
  return 1
}

main "$@"
