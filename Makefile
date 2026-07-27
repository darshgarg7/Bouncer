VENV ?= .venv

ifneq ($(wildcard $(VENV)/bin/python),)
PYTHON ?= $(VENV)/bin/python
RUFF ?= $(VENV)/bin/ruff
MYPY ?= $(VENV)/bin/mypy
else
PYTHON ?= python3
RUFF ?= ruff
MYPY ?= mypy
endif

.PHONY: bootstrap build check containers coverage format-check fuzz-smoke lint test test-go test-python validate-contracts verify-policy-parity project-example evaluate-synthetic evaluate-ablation evaluate-projector evaluate-mechanisms evaluate-ope-simulation

bootstrap:
	./tools/bootstrap.sh "$(VENV)"

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

test: validate-contracts test-go test-python

check: test format-check lint build

coverage:
	./tools/check_go_coverage.sh

fuzz-smoke:
	go test -run '^$$' -fuzz FuzzDecodeBeamNeverPanics -fuzztime 5s ./internal/action
	go test -run '^$$' -fuzz FuzzRankNeverPanics -fuzztime 5s ./internal/router

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
