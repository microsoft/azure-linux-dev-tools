#!/usr/bin/env python3
# Copyright (c) 2026 Microsoft Corporation.
# Licensed under the MIT License.

r"""Evaluate and compare SRPM EVRs inside a mock chroot.

Usage::

    python3 process.py extract <staging_dir> <max_workers>
    python3 process.py compare <staging_dir> <max_workers>

The ``extract`` mode reads ``extract-inputs.json`` and writes
``extract-results.json``. The ``compare`` mode reads ``comparison-inputs.json``
and writes ``comparison-results.json``. Both modes report progress on stderr as
``PROGRESS <completed>/<total> <component>`` and record individual failures in
the output JSON rather than failing the whole batch.
"""

from __future__ import annotations

import json
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path
from typing import TYPE_CHECKING, cast

import rpm

if TYPE_CHECKING:
    from collections.abc import Callable
    from typing import Any

    ManifestItem = dict[str, object]
    Worker = Callable[[ManifestItem], dict[str, object]]

EXPECTED_ARG_COUNT = 4
INCOMPLETE_EVR_MESSAGE = "rpmspec output did not contain a complete EVR"
MAX_ERROR_DETAILS = 4096
QUERY_FORMAT = "epoch=%{epoch}\\nversion=%{version}\\nrelease=%{release}\\n"
RPM_MODULE = cast("Any", rpm)


def error_text(prefix: str, stderr: str) -> str:
    """Return a bounded error message so one bad spec cannot bloat results."""
    details = stderr.strip()
    if len(details) > MAX_ERROR_DETAILS:
        details = details[-MAX_ERROR_DETAILS:]
    return f"{prefix}: {details}" if details else prefix


def parse_evr(output: str) -> dict[str, str]:
    """Parse the intentionally labelled rpmspec query output."""
    fields: dict[str, str] = {}
    for raw_line in output.splitlines():
        key, separator, value = raw_line.partition("=")
        if separator and key in {"epoch", "version", "release"}:
            fields[key] = value

    if not fields.get("version") or not fields.get("release"):
        raise ValueError(INCOMPLETE_EVR_MESSAGE)

    if fields.get("epoch") in {None, "", "(none)"}:
        fields["epoch"] = "0"

    return {"epoch": fields["epoch"], "version": fields["version"], "release": fields["release"]}


def extract_one(staging_dir: str, item: ManifestItem) -> dict[str, object]:
    """Evaluate one rendered spec with the component's configured build options."""
    component = str(item["component"])
    spec_path = Path(staging_dir) / str(item["specPath"])
    spec_dir = spec_path.parent

    command = [
        "rpmspec",
        "-q",
        "--srpm",
        "-D",
        f"_sourcedir {spec_dir}",
        "-D",
        f"_specdir {spec_dir}",
        "-D",
        "with_check 0",
        "--queryformat",
        QUERY_FORMAT,
    ]

    for feature in cast("list[object]", item.get("with", [])):
        command.extend(["--with", str(feature)])

    for feature in cast("list[object]", item.get("without", [])):
        command.extend(["--without", str(feature)])

    defines = cast("dict[str, object]", item.get("defines", {}))
    for key, value in sorted(defines.items()):
        command.extend(["-D", f"{key} {value}"])

    # Keep the same precedence as buildMacrosMap: undefines apply last.
    for macro_name in cast("list[object]", item.get("undefines", [])):
        command.extend(["--undefine", str(macro_name)])

    command.append(str(spec_path))
    completed = subprocess.run(command, capture_output=True, text=True, check=False)
    if completed.returncode != 0:
        return {
            "component": component,
            "evr": None,
            "error": error_text("rpmspec failed", completed.stderr),
        }

    try:
        return {"component": component, "evr": parse_evr(completed.stdout), "error": None}
    except ValueError as exc:
        return {"component": component, "evr": None, "error": str(exc)}


def compare_one(item: ManifestItem) -> dict[str, object]:
    """Compare one EVR pair using the target distro's RPM implementation."""
    component = str(item["component"])
    try:
        previous = cast("dict[str, str]", item["previous"])
        current = cast("dict[str, str]", item["current"])
        comparison = RPM_MODULE.labelCompare(
            (previous["epoch"], previous["version"], previous["release"]),
            (current["epoch"], current["version"], current["release"]),
        )
    except Exception as exc:  # noqa: BLE001 - one malformed EVR must not abort the batch
        return {"component": component, "compare": None, "error": str(exc)}
    else:
        return {"component": component, "compare": comparison, "error": None}


def process_all(
    items: list[ManifestItem],
    max_workers: int,
    worker: Worker,
) -> list[dict[str, object]]:
    """Run a worker in parallel and preserve the manifest's stable ordering."""
    total = len(items)
    completed_results: dict[str, dict[str, object]] = {}

    with ThreadPoolExecutor(max_workers=max_workers) as pool:
        futures = {pool.submit(worker, item): str(item["component"]) for item in items}
        for index, future in enumerate(as_completed(futures), 1):
            component = futures[future]
            try:
                completed_results[component] = future.result()
            except Exception as exc:  # noqa: BLE001 - record worker failures per component
                completed_results[component] = {"component": component, "error": str(exc)}

            print(f"PROGRESS {index}/{total} {component}", file=sys.stderr, flush=True)

    return [completed_results[str(item["component"])] for item in items]


def main() -> int:
    """Execute the requested batch operation and write its JSON result file."""
    if len(sys.argv) != EXPECTED_ARG_COUNT:
        print(f"usage: {sys.argv[0]} <extract|compare> <staging_dir> <max_workers>", file=sys.stderr)
        return 1

    mode, staging_dir, workers_text = sys.argv[1:]
    if mode not in {"extract", "compare"}:
        print(f"unsupported mode: {mode}", file=sys.stderr)
        return 1

    staging_path = Path(staging_dir)
    max_workers = max(1, int(workers_text))
    input_path = staging_path / ("extract-inputs.json" if mode == "extract" else "comparison-inputs.json")
    output_path = staging_path / ("extract-results.json" if mode == "extract" else "comparison-results.json")

    with input_path.open() as input_file:
        items = cast("list[ManifestItem]", json.load(input_file))

    worker = (lambda item: extract_one(staging_dir, item)) if mode == "extract" else compare_one
    results = process_all(items, max_workers, worker)

    with output_path.open("w") as output_file:
        json.dump(results, output_file)

    return 0


if __name__ == "__main__":
    sys.exit(main())
