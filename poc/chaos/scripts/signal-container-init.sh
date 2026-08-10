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
  if [[ "$#" -ne 2 ]]; then
    echo "usage: signal-container-init.sh <container-id> <KILL|TERM>" >&2
    return 2
  fi

  local container_id="$1"
  local signal_name="$2"
  if [[ "${signal_name}" != "KILL" && "${signal_name}" != "TERM" ]]; then
    echo "signal must be KILL or TERM" >&2
    return 2
  fi

  local host_pid
  host_pid=$(crictl inspect --output go-template --template '{{.info.pid}}' "${container_id}")
  if [[ ! "${host_pid}" =~ ^[0-9]+$ ]] || [[ "${host_pid}" -le 1 ]]; then
    echo "invalid container host PID: ${host_pid}" >&2
    return 1
  fi
  local comm
  comm=$(<"/proc/${host_pid}/comm")
  printf 'host_pid=%s\ncomm=%s\nsignal=%s\n' "${host_pid}" "${comm}" "${signal_name}"
  if [[ "${comm}" != "patroni" ]]; then
    echo "refusing to signal container init whose comm is ${comm}" >&2
    return 1
  fi
  kill -s "${signal_name}" "${host_pid}"
}

main "$@"
