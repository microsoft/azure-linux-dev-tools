# Pull Requests and CI Checks

## azldev Contributions

Validate changes to this repository with the normal Mage targets described in
[the development workflow](dev-workflow.md). The pull request workflows run
the corresponding build, test, formatting, static-analysis, and documentation
checks.

## Downstream Rendered-Spec Gate

`azldev component check --evr` is a downstream distro CI check. It is not part
of the normal local component edit, render, or build workflow, and it must not
be added as a required developer preflight step.

Run it in the same mock-capable CI environment as the existing rendered-spec
and lock checks. It compares `--from` directly with `--to`, so CI must choose a
comparison baseline appropriate for the checked-out PR ref and pass it as
`BASELINE_REF`:

```bash
azldev component check --evr --from "$BASELINE_REF" --to HEAD -a -q -O json
```

The command emits a complete JSON report and exits non-zero only when a
rendered spec changed without an SRPM EVR increase, an SRPM EVR decreased, or
an EVR could not be evaluated. Components added or deleted between refs are
accepted. Preserve the JSON result as a CI artifact or include its failing
components in the check output.

| Status | CI meaning |
| --- | --- |
| `bumped` | The current SRPM EVR is greater than the baseline EVR. This passes, including a deliberate release-only rebuild trigger. |
| `already-current` | No monotonicity violation exists: an unchanged spec has an equal EVR, or the component was added or deleted. This passes. |
| `needs-attention` | CI fails: rendered spec content changed without an EVR increase, the EVR decreased, or staging/evaluation/comparison was unsafe or failed. The result's `notes` field explains why. |

The command evaluates rendered specs in the target mock chroot, so the CI job
must execute it in the distro project checkout and use the same containerized,
mock-capable pattern as the existing rendered-spec checks. Do not replace the
existing rendered-spec drift or lock-freshness checks with this gate; it checks
release monotonicity only.
