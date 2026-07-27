#!/usr/bin/env bash
set -euo pipefail

# Keep Python tooling isolated from the developer's global environment. The
# destination can be overridden for editors or CI systems that manage venvs in
# a shared cache.
repository_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
environment_path="${1:-$repository_root/.venv}"
python_command="${PYTHON:-python3}"
lock_file="$repository_root/requirements-dev.lock"

if ! "$python_command" -c 'import sys; raise SystemExit(sys.version_info < (3, 11))'; then
  echo "$python_command must be Python 3.11 or newer" >&2
  exit 1
fi
if [[ -x "$environment_path/bin/python" ]] && \
  ! "$environment_path/bin/python" -c 'import sys; raise SystemExit(sys.version_info < (3, 11))'; then
  echo "existing environment uses Python $("$environment_path/bin/python" --version 2>&1)" >&2
  echo "recreate $environment_path with Python 3.11 or newer, or choose another VENV path" >&2
  exit 1
fi

"$python_command" -m venv "$environment_path"
"$environment_path/bin/python" -m pip install --require-hashes -r "$lock_file"
"$environment_path/bin/python" -m pip install --no-deps --no-build-isolation -e "$repository_root"

echo "Bouncer development environment is ready at $environment_path"
echo "Run 'make check'; the Makefile will use this environment automatically."
