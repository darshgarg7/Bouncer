VENV ?= .venv
.DEFAULT_GOAL := help

ifneq ($(wildcard $(VENV)/bin/python),)
PYTHON ?= $(VENV)/bin/python
RUFF ?= $(VENV)/bin/ruff
MYPY ?= $(VENV)/bin/mypy
else
PYTHON ?= python3
RUFF ?= ruff
MYPY ?= mypy
endif

.PHONY: help architecture-check bootstrap build check containers coverage demo demo-gif format format-check fuzz-smoke lint lock-python mutation-check portfolio-check release-artifacts release-audit release-check test test-go test-python validate-contracts verify-policy-parity project-example evaluate-synthetic evaluate-ablation evaluate-projector evaluate-mechanisms evaluate-ope-simulation evaluate-learning

help:
	@echo "Common targets:"
	@echo "  architecture-check  Enforce authoritative runtime dependency direction"
	@echo "  bootstrap  Create .venv and install development dependencies"
	@echo "  check      Run tests, formatting checks, linters, and builds"
	@echo "  release-check  Run every publication gate, including evidence audits"
	@echo "  portfolio-check  Keep recruiter-facing claims synchronized with evidence"
	@echo "  release-artifacts  Build portable archives, checksums, and test reports"
	@echo "  format     Apply Go and Python formatting"
	@echo "  coverage   Enforce the current Go coverage ratchet"
	@echo "  demo       Run five control-boundary checks without credentials or Docker"
	@echo "  demo-gif   Render the live demo output to docs/assets/bouncer-demo.gif"
	@echo "  build      Build command binaries under bin/"
	@echo "  fuzz-smoke Run short trust-boundary fuzz sessions"
	@echo "  mutation-check  Gate critical Go packages using a separately installed mutator"
	@echo "  lock-python  Regenerate the hash-pinned Python 3.11 development lock"
	@echo "  evaluate-learning  Run the known-truth ML routing simulator"

bootstrap:
	./tools/bootstrap.sh "$(VENV)"

lock-python:
	docker run --rm -v "$(CURDIR):/workspace" -w /workspace python:3.11-slim \
		sh -c 'python -m pip install pip-tools==7.6.0 >/dev/null && pip-compile pyproject.toml --extra dev --generate-hashes --strip-extras --allow-unsafe --resolver=backtracking --output-file requirements-dev.lock'

build:
	mkdir -p bin
	go build -o bin/bouncer-harness ./cmd/bouncer-harness
	go build -o bin/bouncer-run ./cmd/bouncer-run
	go build -o bin/bouncer-provider-gate ./cmd/bouncer-provider-gate
	go build -o bin/bouncer-sandbox ./cmd/bouncer-sandbox
	go build -o bin/bouncer-verify-log ./cmd/bouncer-verify-log

containers:
	docker build -f Dockerfile.bouncer -t bouncer:local .
	docker build -f Dockerfile.sandbox -t bouncer-sandbox:local .

demo:
	go run ./cmd/bouncer-demo

demo-gif:
	$(PYTHON) tools/render_demo_gif.py

test: validate-contracts test-go test-python

check: test format-check lint architecture-check build

architecture-check:
	$(PYTHON) tools/check_architecture.py

release-check: check portfolio-check release-audit

portfolio-check: coverage
	$(PYTHON) tools/check_portfolio.py

release-artifacts:
	./tools/build_release.sh

release-audit:
	$(PYTHON) tools/release_audit.py

coverage:
	./tools/check_go_coverage.sh

format:
	gofmt -w cmd internal
	$(RUFF) format .
	$(RUFF) check --fix .

fuzz-smoke:
	go test -run '^$$' -fuzz FuzzDecodeBeamNeverPanics -fuzztime "$${BOUNCER_FUZZ_TIME:-10s}" ./internal/action
	go test -run '^$$' -fuzz FuzzRankNeverPanics -fuzztime "$${BOUNCER_FUZZ_TIME:-10s}" ./internal/router
	go test -run '^$$' -fuzz FuzzVerifyNeverPanics -fuzztime "$${BOUNCER_FUZZ_TIME:-10s}" ./internal/eventlog
	go test -run '^$$' -fuzz FuzzArtifactValidationNeverPanics -fuzztime "$${BOUNCER_FUZZ_TIME:-10s}" ./internal/learning
	go test -run '^$$' -fuzz FuzzArtifactValidationNeverPanics -fuzztime "$${BOUNCER_FUZZ_TIME:-10s}" ./internal/anomaly
	go test -run '^$$' -fuzz FuzzVirtualPathNormalization -fuzztime "$${BOUNCER_FUZZ_TIME:-10s}" ./internal/policy
	go test -run '^$$' -fuzz FuzzIdempotencyCollisionRejected -fuzztime "$${BOUNCER_FUZZ_TIME:-10s}" ./internal/sandbox

mutation-check:
	test -n "$(MUTATION_TOOL)" || (echo "set MUTATION_TOOL to the go-mutesting executable" >&2; exit 2)
	"$(MUTATION_TOOL)" --noop --coverage --min-covered-msi 70 --logger-summary-json --quiet \
		internal/policy/evaluator.go internal/router/router.go internal/eventlog/jsonl.go

format-check:
	test -z "$$(gofmt -l cmd internal)"
	$(RUFF) format --check .

lint:
	go vet ./...
	$(RUFF) check .
	$(MYPY) constraint_projection benchmarking

test-go:
	go test -race ./...

test-python:
	$(PYTHON) -m unittest discover -s tests -v

validate-contracts:
	$(PYTHON) tools/validate_contracts.py

verify-policy-parity:
	BOUNCER_POLICY_PARITY_CASES=100000 go test -run TestEvaluatorMatchesPythonReferenceAcrossGeneratedCases ./internal/policy

project-example:
	$(PYTHON) -m constraint_projection --input examples/projection-input.json --format json

evaluate-synthetic:
	$(PYTHON) -m benchmarking.evaluate

evaluate-ablation:
	$(PYTHON) -m benchmarking.ablate

evaluate-projector:
	$(PYTHON) -m benchmarking.projector_ablate

evaluate-mechanisms:
	$(PYTHON) -m benchmarking.mechanism_evaluate

evaluate-ope-simulation:
	$(PYTHON) -m benchmarking.ope_simulation --output benchmarks/results/ope-simulation.json

evaluate-learning:
	$(PYTHON) -m benchmarking.learning.evaluate --output benchmarks/results/learning-simulation.json
