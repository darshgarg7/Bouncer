"""Command-line entry point for the deterministic local model simulator."""

from __future__ import annotations

import argparse
import signal
import threading

from .mock_nim import start_mock_nim


def main() -> int:
    """Start the simulator and serve requests until the process is interrupted."""
    parser = argparse.ArgumentParser(description="Run Bouncer's deterministic NIM simulator")
    parser.add_argument("--scenarios", default="benchmarks/scenarios.json")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8001)
    arguments = parser.parse_args()
    stop = threading.Event()

    def handle_signal(_signum: int, _frame: object) -> None:
        stop.set()

    signal.signal(signal.SIGINT, handle_signal)
    signal.signal(signal.SIGTERM, handle_signal)
    with start_mock_nim(arguments.scenarios, arguments.host, arguments.port) as server:
        print(server.endpoint, flush=True)
        stop.wait()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
