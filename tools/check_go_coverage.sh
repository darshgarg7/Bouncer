#!/usr/bin/env bash
set -euo pipefail

overall_minimum="${BOUNCER_OVERALL_COVERAGE_MINIMUM:-60}"
critical_minimum="${BOUNCER_CRITICAL_COVERAGE_MINIMUM:-80}"
coverage_directory="${TMPDIR:-/tmp}/bouncer-coverage"
mkdir -p "$coverage_directory"

check_package() {
  package="$1"
  minimum="$2"
  output="$coverage_directory/$(basename "$package").out"
  go test -coverprofile="$output" "$package" >/dev/null
  percent="$(go tool cover -func="$output" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
  awk -v package="$package" -v actual="$percent" -v minimum="$minimum" 'BEGIN {
    printf "%s coverage: %.1f%% (minimum %.1f%%)\n", package, actual, minimum
    if (actual + 0 < minimum + 0) exit 1
  }'
}

overall_output="$coverage_directory/all.out"
go test -coverprofile="$overall_output" ./... >/dev/null
overall_percent="$(go tool cover -func="$overall_output" | awk '/^total:/ {gsub(/%/, "", $3); print $3}')"
awk -v actual="$overall_percent" -v minimum="$overall_minimum" 'BEGIN {
  printf "overall coverage: %.1f%% (minimum %.1f%%)\n", actual, minimum
  if (actual + 0 < minimum + 0) exit 1
}'

check_package ./internal/policy "$critical_minimum"
check_package ./internal/router "$critical_minimum"
check_package ./internal/executor "$critical_minimum"
