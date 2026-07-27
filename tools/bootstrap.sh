#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
environment_path="${1:-$repository_root/.venv}"
python_command="${PYTHON:-python3}"

"$python_command" -m venv "$environment_path"
"$environment_path/bin/python" -m pip install --upgrade pip
"$environment_path/bin/python" -m pip install -e "$repository_root[dev]"

echo "Bouncer development environment is ready at $environment_path"
echo "Run 'make check'; the Makefile will use this environment automatically."
