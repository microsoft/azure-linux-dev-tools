# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
# ruff: noqa: INP001 -- This script is embedded in the Go binary, not a Python package.

"""Return the patch selection for azldev's vetted, unconditional prep lines."""

from __future__ import annotations

import json
import sys
from contextlib import redirect_stdout
from importlib import import_module
from importlib.util import find_spec


class QuietOutput:
    """Discard unrelated findings from SpecCheck."""

    def __init__(self) -> None:
        """Start with no configured diagnostic details."""
        self.error_details: dict[str, str] = {}

    def add_info(self, *_args: object) -> None:
        """Ignore individual lint messages."""


def main() -> None:
    """Run the lexical check without expanding macros or invoking RPM."""
    if find_spec("rpmlint") is None:
        json.dump({"unavailable": True}, sys.stdout)
        return

    lines: list[str] = json.load(sys.stdin)
    with redirect_stdout(sys.stderr):
        config = import_module("rpmlint.config").Config()
        check = import_module("rpmlint.checks.SpecCheck").SpecCheck(config, QuietOutput())
        check.pkg = import_module("rpmlint.pkg").FakePkg("azldev.spec")
        check.spec_only = True
        # SpecCheck has no public interface for just its lexical selection pass.
        check._check_lines(["%prep\n", *(line + "\n" for line in lines)])  # noqa: SLF001

    json.dump(
        {"all": check.patches_auto_applied, "numbers": sorted(set(check.applied_patches))},
        sys.stdout,
    )


if __name__ == "__main__":
    main()
