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

main() {
  if [[ "$#" -lt 1 || "$#" -gt 2 ]]; then
    echo "usage: inspect-container.sh <container-id> [old-cgroup-path]" >&2
    return 2
  fi

  local container_id="$1"
  local host_pid
  host_pid=$(crictl inspect --output go-template --template '{{.info.pid}}' "${container_id}")
  if [[ ! "${host_pid}" =~ ^[0-9]+$ ]] || [[ "${host_pid}" -le 1 ]]; then
    echo "invalid container host PID: ${host_pid}" >&2
    return 1
  fi

  local comm
  local cgroup
  local cmdline
  local processes
  local zombie_count
  comm=$(<"/proc/${host_pid}/comm")
  cgroup=$(awk -F: '$1 == "0" {print $3}' "/proc/${host_pid}/cgroup")
  cmdline=$(tr '\000' ' ' <"/proc/${host_pid}/cmdline")
  processes=$(nsenter -t "${host_pid}" -p -m -- ps -eo pid,ppid,stat,comm)
  zombie_count=$(awk 'NR > 1 && $3 ~ /^Z/ {count++} END {print count + 0}' <<<"${processes}")
  printf 'host_pid=%s\ncomm=%s\ncgroup=%s\ncmdline=%s\nzombie_count=%s\nprocesses_begin\n%s\n' \
    "${host_pid}" "${comm}" "${cgroup}" "${cmdline}" "${zombie_count}" "${processes}"

  if [[ "$#" -eq 2 ]]; then
    local old_cgroup="$2"
    if [[ ! "${old_cgroup}" =~ ^/[A-Za-z0-9_.:/-]+$ || "${old_cgroup}" == *..* ]]; then
      echo "invalid old cgroup path" >&2
      return 2
    fi
    local old_count=0
    local old_procs="/sys/fs/cgroup${old_cgroup}/cgroup.procs"
    if [[ -r "${old_procs}" ]]; then
      local old_pid
      while IFS= read -r old_pid; do
        if [[ -r "/proc/${old_pid}/comm" ]] && [[ "$(<"/proc/${old_pid}/comm")" == postgres* ]]; then
          old_count=$((old_count + 1))
        fi
      done <"${old_procs}"
    fi
    printf 'old_cgroup_postgres_count=%s\n' "${old_count}"
  fi
}

main "$@"
