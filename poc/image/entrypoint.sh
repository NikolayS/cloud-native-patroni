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

# The top-level body lets the final exec replace the shell directly; a main function is absent.
umask 0077

# The PVC mount is writable by uid 999 when the Pod supplies fsGroup 999.
mkdir -p "${PGDATA}"
chmod 0700 "${PGDATA}"

# Render credentials safely before Patroni reads its configuration.
cnpatroni-render-config

# The Pod manifest overrides this with the ConfigMap entrypoint in
# poc/manifests/cluster/20-config.yaml, which is what the cluster runs. This file
# is exercised by poc/image/test/run-contract-tests.sh; the two renderers use
# different paths on purpose.
# Patroni is the sole high-availability authority and becomes literal PID 1.
exec patroni "$CNPATRONI_CONFIG_FILE"
