# Contributing

## Development setup

```bash
python3 -m venv .venv
. .venv/bin/activate
python3 -m pip install -e '.[dev]'
make test
```

## Required checks

Before proposing a change:

```bash
make test
go vet ./...
ruff check .
mypy constraint_projection benchmarking
```

New Go logic requires table-driven tests where practical. New constraint behavior requires Python unit tests and at least one cross-language bridge test when it changes the wire result.

## Protocol changes

Do not change a schema, action meaning, constraint code, objective direction, or selection rule silently.

A protocol change must include:

- schema and documentation updates;
- Go and Python test fixtures;
- a compatibility decision;
- a benchmark-version decision; and
- regenerated evaluation artifacts when results remain comparable.

## Benchmark integrity

- Do not tune against held-out tasks and then report them as held out.
- Do not remove failed runs from aggregate results.
- Keep simulator and real-provider results separate.
- Freeze thresholds before collecting comparable runs.
- Count all model calls and tokens.
- Label synthetic token accounting as synthetic.
- Preserve negative results.

## Style

- Go is formatted with `gofmt` and checked with `go vet`.
- Python targets 3.11+, uses type annotations, and is checked with Ruff and mypy.
- JSON fixtures use two-space indentation and end with a newline.
- Documentation distinguishes implemented behavior, planned behavior, and experimental evidence.

## Safety changes

Hard constraints may become stricter through an ordinary reviewed change. A learned component must never receive the ability to weaken a hard constraint. Any proposal to execute outside the virtual MVB requires a dedicated threat model and sandbox review.
