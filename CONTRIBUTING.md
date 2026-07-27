# Contributing

Thanks for taking a look at Bouncer. The repository is a research prototype,
so a good change should do two things: keep the runtime easy to inspect and
keep every public claim narrower than the evidence behind it.

## Set up the repository

You need Go 1.23 or newer and Python 3.11 or newer.

```bash
make bootstrap
make check
```

`make bootstrap` creates `.venv` and installs the development dependencies.
The remaining Make targets use that environment automatically.

## Before changing code

Start with the short [development guide](docs/DEVELOPMENT.md). It identifies
the runtime path, the independent Python reference, and the tests that protect
each boundary.

Keep these invariants in mind:

- model output is untrusted input;
- only the Go policy evaluator can admit an action;
- routing sees policy-passing candidates only;
- execution must match the deterministic transition contract; and
- offline analysis cannot grant permission.

If a proposed change weakens one of these rules, open a design discussion
before implementing it.

## Make a focused change

- Prefer small packages and plain data structures over framework abstractions.
- Add comments for constraints, trade-offs, and surprising behavior. Do not
  restate code that is already clear.
- Use PEP 8 formatting and PEP 257 docstrings for production Python. Ruff
  enforces both.
- Keep Go package comments in each package's `doc.go`; document exported APIs
  when their contract is not obvious from the type.
- Treat generated benchmark reports as evidence. Do not hand-edit their
  numbers.
- Never put credentials, prompt bodies, or private task state in fixtures or
  logs.

## Run the relevant gates

For ordinary changes:

```bash
make check
make coverage
```

For policy changes, also run:

```bash
make verify-policy-parity
```

For candidate decoding or routing changes, also run:

```bash
make fuzz-smoke
```

If a benchmark result changes, regenerate the affected report and update
`docs/CLAIMS.md` in the same change. State clearly whether the evidence is a
unit test, synthetic integration run, hosted-provider run, or external
evaluation.

## Change a protocol deliberately

Do not silently change a schema, action meaning, constraint code, objective
direction, or selection rule. A protocol change needs:

- matching schema and documentation updates;
- Go and Python fixtures where both implementations share the contract;
- an explicit compatibility and versioning decision; and
- regenerated evaluation artifacts when the old and new results remain
  comparable.

Hard constraints may become stricter through an ordinary reviewed change. A
learned component must never gain the ability to weaken a hard constraint. Any
proposal to execute outside the virtual benchmark needs a dedicated threat
model and sandbox review.

## Protect benchmark integrity

- Do not tune against held-out tasks and then describe them as held out.
- Do not remove failed runs from aggregate results.
- Keep simulator and real-provider results separate.
- Freeze thresholds before collecting comparable runs.
- Count every model call and provider token.
- Label synthetic token accounting as synthetic.
- Preserve negative results.

## Commit hygiene

Do not commit `.env`, local manifests, `bin/`, `.venv/`, or files under
`benchmarks/results/`. Before handing off a change, check:

```bash
git status --short
git diff --check
```

Explain what behavior changed, how it was tested, and which evidence claims—if
any—were affected.
