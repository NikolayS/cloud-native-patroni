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

readonly chain_in="CNPATRONI_CHAOS_IN"
readonly chain_out="CNPATRONI_CHAOS_OUT"

remove_chain() {
  while iptables -w -C INPUT -j "${chain_in}" 2>/dev/null; do
    iptables -w -D INPUT -j "${chain_in}"
  done
  while iptables -w -C OUTPUT -j "${chain_out}" 2>/dev/null; do
    iptables -w -D OUTPUT -j "${chain_out}"
  done
  iptables -w -F "${chain_in}" 2>/dev/null || true
  iptables -w -F "${chain_out}" 2>/dev/null || true
  iptables -w -X "${chain_in}" 2>/dev/null || true
  iptables -w -X "${chain_out}" 2>/dev/null || true
}

add_peer_rules() {
  local values="$1"
  local -a peers=()
  local peer
  IFS=',' read -r -a peers <<<"${values}"
  for peer in "${peers[@]}"; do
    [[ -n "${peer}" ]] || continue
    iptables -w -A "${chain_out}" -d "${peer}" -j DROP
    iptables -w -A "${chain_in}" -s "${peer}" -j DROP
  done
}

apply_partition() {
  if [[ "$#" -ne 5 ]]; then
    echo "usage: partition-node.sh apply <oracle-ip> <control-plane-ip> <peer-node-csv> <peer-pod-cidr-csv> <service-api-ip>" >&2
    return 2
  fi
  local oracle_ip="$1"
  local control_plane_ip="$2"
  local peer_nodes="$3"
  local peer_cidrs="$4"
  local service_api_ip="$5"

  remove_chain
  iptables -w -N "${chain_in}"
  iptables -w -N "${chain_out}"
  iptables -w -I INPUT 1 -j "${chain_in}"
  iptables -w -I OUTPUT 1 -j "${chain_out}"

  iptables -w -A "${chain_in}" -s "${oracle_ip}" -j RETURN
  iptables -w -A "${chain_out}" -d "${oracle_ip}" -j RETURN
  iptables -w -A "${chain_out}" -p tcp -d "${control_plane_ip}" --dport 6443 -j DROP
  iptables -w -A "${chain_in}" -p tcp -s "${control_plane_ip}" --sport 6443 -j DROP
  iptables -w -A "${chain_out}" -p tcp -d "${service_api_ip}" --dport 443 -j DROP
  add_peer_rules "${peer_nodes}"
  add_peer_rules "${peer_cidrs}"
  iptables -w -L "${chain_in}" -n -v
  iptables -w -L "${chain_out}" -n -v
}

main() {
  if [[ "$#" -lt 1 ]]; then
    echo "usage: partition-node.sh <apply|heal> ..." >&2
    return 2
  fi
  local action="$1"
  shift
  case "${action}" in
    apply)
      apply_partition "$@"
      ;;
    heal)
      if [[ "$#" -ne 0 ]]; then
        echo "heal accepts no arguments" >&2
        return 2
      fi
      remove_chain
      ;;
    *)
      echo "action must be apply or heal" >&2
      return 2
      ;;
  esac
}

main "$@"
