#!/usr/bin/env bash
#
# Copyright © contributors to CloudNativePG, established as
# CloudNativePG a Series of LF Projects, LLC.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# SPDX-License-Identifier: Apache-2.0

set -Eeuo pipefail
IFS=$'\n\t'

readonly DEADCODE_TOOL="golang.org/x/tools/cmd/deadcode@v0.48.0"
readonly DUPL_TOOL="github.com/mibk/dupl@v1.1.0"

# Each index declares one scan unit: its module, repository-relative scan
# directories, deadcode arguments, and the reason for those roots. Add a row to
# all four arrays when fork-owned Go code needs a new scan unit. Multiple scan
# directories or deadcode arguments in one row are newline-delimited.
readonly -a SCAN_UNIT_MODULES=(
  "hack/cnpatroni/upstream"
  "hack/cnpatroni/audit"
  "poc/oracle"
  "poc/chaos"
  "."
)
readonly -a SCAN_UNIT_DIRECTORIES=(
  "hack/cnpatroni/upstream"
  "hack/cnpatroni/audit"
  "poc/oracle"
  "poc/chaos"
  "internal/cnpatroni"
)
readonly -a SCAN_UNIT_DEADCODE_ARGUMENTS=(
  "./cmd/..."
  "."
  "./cmd/..."
  "./cmd/..."
  $'-test\n-filter=^github\\.com/cloudnative-pg/cloudnative-pg/internal/cnpatroni(?:/.*)?$\n./internal/cnpatroni/...'
)
readonly -a SCAN_UNIT_NOTES=(
  "The command packages are the executable roots; internal/gittest is a test-only fixture."
  "The audit module currently has one command package at its module root."
  "The walking-skeleton write oracle; cmd/oracle is its executable root."
  "The walking-skeleton chaos harness; cmd/chaos is its executable root."
  "The library test executable supplies the root; the filter keeps findings in scope."
)

# deadcode needs an executable root. internal/cnpatroni is a library, so its
# test executable supplies that root; the filter keeps findings in scope.
# Widening the upstream roots to ./... is not valid: it falsely reports all 13
# internal/gittest helpers that six test files import. Using -test ./... is also
# not valid: go test makes every package a root, so deadcode reports nothing.

# These are the only Go modules inherited from upstream. Every other module is
# fork-authored by construction and must be covered by a declared scan unit.
readonly -a INHERITED_MODULES=(
  "."
  "tests/"
)
readonly -a INHERITED_MODULE_NOTES=(
  "The upstream operator module; only its fork-owned subtrees are in scope."
  "The upstream end-to-end suite."
)

# Sixty tokens is the measured zero-clone threshold. It is high enough to avoid
# flagging short, idiomatic Go sequences such as repeated error handling, but
# low enough to catch a substantial copied block before it becomes entrenched.
readonly DUPL_THRESHOLD=60

hygiene_tmp=""

cleanup() {
  if [[ -n "${hygiene_tmp}" ]]; then
    rm -rf -- "${hygiene_tmp}"
  fi
}

print_tool_failure() {
  local analysis="$1"
  local standard_output="$2"
  local standard_error="$3"

  printf '%s could not run:\n' "${analysis}" >&2
  if [[ -s "${standard_output}" ]]; then
    cat "${standard_output}" >&2
  fi
  if [[ -s "${standard_error}" ]]; then
    cat "${standard_error}" >&2
  fi
}

file_contains_line() {
  local expected="$1"
  local file="$2"
  local line

  while IFS= read -r line; do
    if [[ "${line}" == "${expected}" ]]; then
      return 0
    fi
  done <"${file}"

  return 1
}

validate_configuration() {
  local scan_unit_count="${#SCAN_UNIT_MODULES[@]}"

  if [[ "${#SCAN_UNIT_DIRECTORIES[@]}" -ne "${scan_unit_count}" ]] ||
    [[ "${#SCAN_UNIT_DEADCODE_ARGUMENTS[@]}" -ne "${scan_unit_count}" ]] ||
    [[ "${#SCAN_UNIT_NOTES[@]}" -ne "${scan_unit_count}" ]]; then
    printf 'Code-hygiene scan-unit table is inconsistent.\n' >&2
    return 1
  fi

  if [[ "${#INHERITED_MODULES[@]}" -ne "${#INHERITED_MODULE_NOTES[@]}" ]]; then
    printf 'Code-hygiene inherited-module table is inconsistent.\n' >&2
    return 1
  fi
}

collect_scanned_files() {
  local repo_root="$1"
  local file_list="$2"

  if ! (
    cd "${repo_root}"
    local index
    local directory
    for index in "${!SCAN_UNIT_MODULES[@]}"; do
      while IFS= read -r directory; do
        if [[ -z "${directory}" ]]; then
          continue
        fi
        if ! find \
          "${directory}" \
          -type f \
          -name '*.go' \
          ! -name '*_test.go' \
          ! -path '*/testdata/*' \
          -print; then
          exit 1
        fi
      done <<<"${SCAN_UNIT_DIRECTORIES[${index}]}"
    done
  ) | LC_ALL=C sort -u >"${file_list}"; then
    printf 'Duplication analysis could not collect its input files.\n' >&2
    return 1
  fi
}

collect_scanned_package_directories() {
  local repo_root="$1"
  local file_list="$2"
  local directory_list="$3"
  local file

  while IFS= read -r file; do
    printf '%s/%s\n' "${repo_root}" "${file%/*}"
  done <"${file_list}" | LC_ALL=C sort -u >"${directory_list}"
}

check_deadcode() {
  local scope="$1"
  local working_directory="$2"
  local output_id="$3"
  shift 3

  local standard_output="${hygiene_tmp}/${output_id}.stdout"
  local standard_error="${hygiene_tmp}/${output_id}.stderr"

  if ! (
    cd "${working_directory}"
    go run "${DEADCODE_TOOL}" "$@"
  ) >"${standard_output}" 2>"${standard_error}"; then
    print_tool_failure "Dead-code analysis for ${scope}" \
      "${standard_output}" "${standard_error}"
    return 1
  fi

  if [[ -s "${standard_error}" ]]; then
    cat "${standard_error}" >&2
  fi
  if [[ -s "${standard_output}" ]]; then
    printf 'Unreachable functions found in %s:\n' "${scope}" >&2
    cat "${standard_output}" >&2
    return 1
  fi

  printf 'Dead code: %s: 0 unreachable functions.\n' "${scope}"
}

is_inherited_module() {
  local module="${1%/}"
  local inherited_module

  for inherited_module in "${INHERITED_MODULES[@]}"; do
    if [[ "${module}" == "${inherited_module%/}" ]]; then
      return 0
    fi
  done

  return 1
}

nearest_module_for_file() {
  local file="$1"
  local module_list="$2"
  local module
  local nearest=""
  local nearest_length=-1

  while IFS= read -r module; do
    if [[ "${module}" == "." ]]; then
      if [[ "${nearest_length}" -lt 0 ]]; then
        nearest="."
        nearest_length=0
      fi
    elif [[ "${file}" == "${module}/"* ]] &&
      [[ "${#module}" -gt "${nearest_length}" ]]; then
      nearest="${module}"
      nearest_length="${#module}"
    fi
  done <"${module_list}"

  if [[ -n "${nearest}" ]]; then
    printf '%s\n' "${nearest}"
  else
    printf '<no-module>\n'
  fi
}

collect_modules() {
  local repo_root="$1"
  local module_list="$2"
  local go_mod_list="${hygiene_tmp}/go-mod-files"
  local go_mod
  local module

  if ! (
    cd "${repo_root}"
    find \
      . \
      -type f \
      -name go.mod \
      ! -path '*/vendor/*' \
      ! -path '*/testdata/*' \
      -print
  ) | LC_ALL=C sort >"${go_mod_list}"; then
    printf 'Coverage assertion could not locate Go modules.\n' >&2
    return 1
  fi

  while IFS= read -r go_mod; do
    go_mod="${go_mod#./}"
    if [[ "${go_mod}" == "go.mod" ]]; then
      module="."
    else
      module="${go_mod%/go.mod}"
    fi
    printf '%s\n' "${module}"
  done <"${go_mod_list}" | LC_ALL=C sort -u >"${module_list}"

  if [[ ! -s "${module_list}" ]]; then
    printf 'Coverage assertion could not locate any Go modules.\n' >&2
    return 1
  fi
}

collect_candidate_files() {
  local repo_root="$1"
  local candidate_list="$2"
  local candidate_nul="$3"
  local all_candidates="${hygiene_tmp}/all-candidate-go-files.nul"
  local file

  if ! git -C "${repo_root}" ls-files \
    -co \
    --exclude-standard \
    -z \
    -- \
    '*.go' >"${all_candidates}"; then
    printf 'Coverage assertion could not enumerate repository Go files.\n' >&2
    return 1
  fi

  : >"${candidate_list}"
  : >"${candidate_nul}"
  while IFS= read -r -d '' file; do
    if [[ "${file}" == *_test.go ]] || [[ "/${file}" == */testdata/* ]]; then
      continue
    fi
    printf '%s\n' "${file}" >>"${candidate_list}"
    printf '%s\0' "${file}" >>"${candidate_nul}"
  done <"${all_candidates}"

  LC_ALL=C sort -u -o "${candidate_list}" "${candidate_list}"
}

check_scan_coverage() {
  local repo_root="$1"
  local scanned_files="$2"
  local candidate_list="${hygiene_tmp}/candidate-go-files"
  local candidate_nul="${hygiene_tmp}/candidate-go-files.nul"
  local attribute_output="${hygiene_tmp}/candidate-attributes.nul"
  local owned_files="${hygiene_tmp}/owned-go-files"
  local module_list="${hygiene_tmp}/go-modules"
  local uncovered="${hygiene_tmp}/uncovered-go-files"
  local uncovered_sorted="${hygiene_tmp}/uncovered-go-files.sorted"
  local checkout_state
  local file
  local attribute
  local value
  local module
  local current_module=""
  local uncovered_file
  local fork_owned_count=0

  if ! checkout_state="$(git -C "${repo_root}" rev-parse --is-inside-work-tree 2>/dev/null)" ||
    [[ "${checkout_state}" != "true" ]]; then
    printf 'Coverage assertion could not run: %s is not a Git checkout.\n' \
      "${repo_root}" >&2
    return 1
  fi

  if ! collect_candidate_files \
    "${repo_root}" "${candidate_list}" "${candidate_nul}"; then
    return 1
  fi
  if ! collect_modules "${repo_root}" "${module_list}"; then
    return 1
  fi

  if ! git -C "${repo_root}" check-attr \
    --stdin \
    -z \
    cnpatroni-boundary \
    <"${candidate_nul}" >"${attribute_output}"; then
    printf 'Coverage assertion could not read cnpatroni-boundary attributes.\n' >&2
    return 1
  fi

  : >"${owned_files}"
  while IFS= read -r -d '' file; do
    if ! IFS= read -r -d '' attribute || ! IFS= read -r -d '' value; then
      printf 'Coverage assertion received malformed git attribute output.\n' >&2
      return 1
    fi
    if [[ "${attribute}" == "cnpatroni-boundary" ]] &&
      [[ "${value}" == "cnpatroni-owned" ]]; then
      printf '%s\n' "${file}" >>"${owned_files}"
    fi
  done <"${attribute_output}"
  LC_ALL=C sort -u -o "${owned_files}" "${owned_files}"

  : >"${uncovered}"
  while IFS= read -r file; do
    module="$(nearest_module_for_file "${file}" "${module_list}")"
    if file_contains_line "${file}" "${owned_files}" ||
      ! is_inherited_module "${module}"; then
      fork_owned_count=$((fork_owned_count + 1))
      if ! file_contains_line "${file}" "${scanned_files}"; then
        printf '%s\t%s\n' "${module}" "${file}" >>"${uncovered}"
      fi
    fi
  done <"${candidate_list}"

  if [[ -s "${uncovered}" ]]; then
    LC_ALL=C sort "${uncovered}" >"${uncovered_sorted}"
    printf 'Fork-owned Go files are outside the declared code-hygiene scan units:\n' >&2
    while IFS=$'\t' read -r module uncovered_file; do
      if [[ "${module}" != "${current_module}" ]]; then
        printf '  Module %s:\n' "${module}" >&2
        current_module="${module}"
      fi
      printf '    %s\n' "${uncovered_file}" >&2
    done <"${uncovered_sorted}"
    printf '%s\n' \
      'This is deliberate fail-closed behaviour, not a bug in the gate.' \
      "Add each path's module and directory to the scan-unit table in" \
      'hack/cnpatroni/check-code-hygiene.sh, choosing dead-code roots appropriate' \
      'to that module.' >&2
    return 1
  fi

  printf 'Coverage: all %s fork-owned Go files are in declared scan units.\n' \
    "${fork_owned_count}"
}

check_orphan_packages() {
  local repo_root="$1"
  local scanned_directories="$2"
  local unit_index="$3"
  local scope="${SCAN_UNIT_DIRECTORIES[${unit_index}]}"
  local module="${SCAN_UNIT_MODULES[${unit_index}]}"
  local working_directory="${repo_root}"
  local output_id="orphan-${unit_index}"
  local imports_output="${hygiene_tmp}/${output_id}.imports.stdout"
  local imports_error="${hygiene_tmp}/${output_id}.imports.stderr"
  local imports="${hygiene_tmp}/${output_id}.imports"
  local packages_output="${hygiene_tmp}/${output_id}.packages.stdout"
  local packages_error="${hygiene_tmp}/${output_id}.packages.stderr"
  local orphans="${hygiene_tmp}/${output_id}.orphans"
  local package_directory
  local import_path
  local package_name
  local has_tests
  local relative_directory

  if [[ "${module}" != "." ]]; then
    working_directory="${repo_root}/${module}"
  fi

  if ! (
    cd "${working_directory}"
    go list \
      -f '{{range .Imports}}{{.}}{{"\n"}}{{end}}{{range .TestImports}}{{.}}{{"\n"}}{{end}}{{range .XTestImports}}{{.}}{{"\n"}}{{end}}' \
      ./...
  ) >"${imports_output}" 2>"${imports_error}"; then
    print_tool_failure "Orphan-package analysis for ${scope}" \
      "${imports_output}" "${imports_error}"
    return 1
  fi
  if [[ -s "${imports_error}" ]]; then
    cat "${imports_error}" >&2
  fi
  LC_ALL=C sort -u "${imports_output}" >"${imports}"

  if ! (
    cd "${working_directory}"
    go list \
      -f $'{{.Dir}}\t{{.ImportPath}}\t{{.Name}}\t{{if or .TestGoFiles .XTestGoFiles}}yes{{else}}no{{end}}' \
      ./...
  ) >"${packages_output}" 2>"${packages_error}"; then
    print_tool_failure "Orphan-package analysis for ${scope}" \
      "${packages_output}" "${packages_error}"
    return 1
  fi
  if [[ -s "${packages_error}" ]]; then
    cat "${packages_error}" >&2
  fi

  : >"${orphans}"
  while IFS=$'\t' read -r package_directory import_path package_name has_tests; do
    if [[ "${package_name}" == "main" ]] ||
      ! file_contains_line "${package_directory}" "${scanned_directories}"; then
      continue
    fi
    if ! file_contains_line "${import_path}" "${imports}" &&
      [[ "${has_tests}" == "no" ]]; then
      relative_directory="${package_directory#"${repo_root}/"}"
      printf '%s\n' "${relative_directory}" >>"${orphans}"
    fi
  done <"${packages_output}"

  if [[ -s "${orphans}" ]]; then
    printf 'Orphan packages found in %s:\n' "${scope}" >&2
    LC_ALL=C sort -u "${orphans}" >&2
    return 1
  fi

  printf 'Orphan packages: %s: 0 packages.\n' "${scope}"
}

check_duplication() {
  local repo_root="$1"
  local file_list="$2"
  local standard_output="${hygiene_tmp}/dupl.stdout"
  local standard_error="${hygiene_tmp}/dupl.stderr"
  local file_count

  file_count="$(wc -l <"${file_list}")"
  file_count="${file_count//[[:space:]]/}"
  if [[ "${file_count}" -eq 0 ]]; then
    printf 'Duplication analysis could not run: no eligible Go files found.\n' >&2
    return 1
  fi

  if ! (
    cd "${repo_root}"
    go run "${DUPL_TOOL}" \
      -plumbing \
      -t "${DUPL_THRESHOLD}" \
      -files <"${file_list}"
  ) >"${standard_output}" 2>"${standard_error}"; then
    print_tool_failure "Duplication analysis" \
      "${standard_output}" "${standard_error}"
    return 1
  fi

  if [[ -s "${standard_error}" ]]; then
    cat "${standard_error}" >&2
  fi
  if [[ -s "${standard_output}" ]]; then
    printf 'Duplicate code found at the following locations:\n' >&2
    cat "${standard_output}" >&2
    return 1
  fi

  printf 'Duplication: 0 clone groups in %s files at the %s-token threshold.\n' \
    "${file_count}" "${DUPL_THRESHOLD}"
}

main() {
  local script_directory
  local repo_root
  local scanned_files
  local scanned_directories
  local scan_files_collected=0
  local failures=0
  local index
  local argument
  local working_directory
  local -a deadcode_arguments

  script_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
  repo_root="$(cd -- "${script_directory}/../.." && pwd)"
  hygiene_tmp="$(mktemp -d "${TMPDIR:-/tmp}/cnpatroni-hygiene.XXXXXX")"
  trap cleanup EXIT
  scanned_files="${hygiene_tmp}/scanned-go-files"
  scanned_directories="${hygiene_tmp}/scanned-package-directories"

  if ! validate_configuration; then
    failures=1
  fi

  if collect_scanned_files "${repo_root}" "${scanned_files}"; then
    scan_files_collected=1
    collect_scanned_package_directories \
      "${repo_root}" "${scanned_files}" "${scanned_directories}"
  else
    failures=1
  fi

  for index in "${!SCAN_UNIT_MODULES[@]}"; do
    deadcode_arguments=()
    while IFS= read -r argument; do
      if [[ -n "${argument}" ]]; then
        deadcode_arguments+=("${argument}")
      fi
    done <<<"${SCAN_UNIT_DEADCODE_ARGUMENTS[${index}]}"

    working_directory="${repo_root}"
    if [[ "${SCAN_UNIT_MODULES[${index}]}" != "." ]]; then
      working_directory="${repo_root}/${SCAN_UNIT_MODULES[${index}]}"
    fi
    if ! check_deadcode \
      "${SCAN_UNIT_DIRECTORIES[${index}]}" \
      "${working_directory}" \
      "deadcode-${index}" \
      "${deadcode_arguments[@]}"; then
      failures=1
    fi
  done

  if [[ "${scan_files_collected}" -eq 1 ]]; then
    if ! check_scan_coverage "${repo_root}" "${scanned_files}"; then
      failures=1
    fi

    for index in "${!SCAN_UNIT_MODULES[@]}"; do
      if ! check_orphan_packages \
        "${repo_root}" "${scanned_directories}" "${index}"; then
        failures=1
      fi
    done

    if ! check_duplication "${repo_root}" "${scanned_files}"; then
      failures=1
    fi
  fi

  if [[ "${failures}" -ne 0 ]]; then
    printf 'CloudNativePatroni code hygiene failed.\n' >&2
    return 1
  fi

  printf 'CloudNativePatroni code hygiene passed.\n'
}

main "$@"
