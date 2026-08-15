# Copyright (c) 2026 Microsoft Corporation.
# Licensed under the MIT License.

"""Test pure EVR processor helpers without requiring the target RPM bindings."""

from __future__ import annotations

import importlib.util
import sys
import unittest
from pathlib import Path
from types import ModuleType
from typing import Protocol, cast
from unittest.mock import patch

PROCESS_PATH = Path(__file__).with_name("process.py")
ERR_LOAD_PROCESSOR_MODULE = "failed to load processor module"


class ProcessModule(Protocol):
    """Describe the pure helpers exported by the processor module."""

    MAX_ERROR_DETAILS: int

    def error_text(self, prefix: str, stderr: str) -> str:
        """Return an error message with bounded stderr details."""
        ...

    def parse_evr(self, output: str) -> dict[str, str]:
        """Parse labelled rpmspec output into an EVR mapping."""
        ...


def load_process_module() -> ProcessModule:
    """Load the processor while substituting its mock-chroot RPM binding."""
    spec = importlib.util.spec_from_file_location("evr_process_under_test", PROCESS_PATH)
    if spec is None or spec.loader is None:
        raise RuntimeError(ERR_LOAD_PROCESSOR_MODULE)

    module = importlib.util.module_from_spec(spec)
    with patch.dict(sys.modules, {"rpm": ModuleType("rpm")}):
        spec.loader.exec_module(module)

    return cast("ProcessModule", module)


PROCESS = load_process_module()


class ProcessPureFunctionsTest(unittest.TestCase):
    """Cover processor helpers that do not need rpmspec or rpm.labelCompare."""

    def test_parse_evr_normalizes_none_epoch(self) -> None:
        """Normalize rpmspec's missing epoch marker to zero."""
        evr = PROCESS.parse_evr("epoch=(none)\nversion=3.123.1\nrelease=1.azl4\n")

        self.assertEqual(  # noqa: PT009
            evr,
            {"epoch": "0", "version": "3.123.1", "release": "1.azl4"},
        )

    def test_error_text_truncates_to_trailing_details(self) -> None:
        """Retain only the bounded tail of oversized rpmspec stderr output."""
        stderr = "discarded-" + ("x" * PROCESS.MAX_ERROR_DETAILS)

        actual = PROCESS.error_text("rpmspec failed", stderr)

        self.assertEqual(  # noqa: PT009
            actual,
            f"rpmspec failed: {stderr[-PROCESS.MAX_ERROR_DETAILS :]}",
        )


if __name__ == "__main__":
    unittest.main()
