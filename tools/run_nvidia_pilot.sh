#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
output_directory="${1:-$repository_root/benchmarks/results/nvidia-pilot-$(date -u +%Y%m%dT%H%M%SZ)}"

if [[ ! -f "$repository_root/.env" ]]; then
  echo "missing $repository_root/.env; copy .env.example and add NVIDIA_API_KEY" >&2
  exit 1
fi
if [[ -e "$output_directory" ]]; then
  echo "output directory already exists: $output_directory" >&2
  exit 1
fi

set -a
# shellcheck disable=SC1091
source "$repository_root/.env"
set +a
: "${NVIDIA_API_KEY:?NVIDIA_API_KEY must be set in .env}"

# Ensure this NVIDIA-specific pilot cannot accidentally use a legacy NIM key
# inherited from the caller's shell.
export NIM_API_KEY="$NVIDIA_API_KEY"

mkdir -p "$output_directory"
make -C "$repository_root" build

for task_number in 001 002 003; do
  task_id="task-$task_number"
  "$repository_root/bin/bouncer-run" \
    -manifest "$repository_root/configs/run-manifest.nvidia-hosted.json" \
    -task "$repository_root/benchmarks/tasks/$task_id.json" \
    -project-root "$repository_root" \
    -objective-calibration "$repository_root/configs/objective-calibration.bootstrap.json" \
    -seed 42 \
    -executor-mode virtual \
    -event-log "$output_directory/$task_id-events.jsonl" \
    -output "$output_directory/$task_id-result.json" \
    >/dev/null
  "$repository_root/bin/bouncer-verify-log" \
    -event-log "$output_directory/$task_id-events.jsonl" \
    >"$output_directory/$task_id-verification.json"
done

"$repository_root/.venv/bin/python" "$repository_root/tools/summarize_pilot.py" \
  "$output_directory" >/dev/null

echo "NVIDIA pilot artifacts written to $output_directory"
