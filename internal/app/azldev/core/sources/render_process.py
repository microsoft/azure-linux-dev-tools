#!/usr/bin/env python3
# Copyright (c) 2026 Microsoft Corporation.
# Licensed under the MIT License.

r"""Run rpmautospec and spectool for each component inside a mock chroot.

Writes per-component results to a JSON file in the staging directory.

This script is embedded in the azldev Go binary and executed inside a mock chroot
during ``azldev component render``. It avoids the need for complex inline shell
scripts which are error-prone.

Usage::

    python3 render_process.py <staging_dir> <max_workers>

The staging directory must contain an ``inputs.json`` file listing components::

    [{"name": "curl", "specFilename": "curl.spec"}, ...]

Results are written to ``<staging_dir>/results.json``::

    [
      {"name": "curl", "specFiles": "Source0: curl-8.5.0.tar.xz\nPatch0: fix.patch", "error": null},
      {"name": "broken", "specFiles": "", "error": "rpmautospec failed: ..."}
    ]

Progress is reported to stderr as ``PROGRESS <completed>/<total> <name>``.
"""

from __future__ import annotations

import json
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

# Number of positional arguments expected on the command line (script, staging_dir, max_workers).
EXPECTED_ARG_COUNT = 3


def process_component(staging_dir: str, name: str, spec_filename: str) -> dict[str, str | None]:
    """Run rpmautospec + spectool for a single component, returning a result dict.

    Trust boundary: name and spec_filename are validated by BatchProcess in
    mockprocessor.go (validateComponentInput rejects path separators, empty
    values, and non-basename spec filenames) before this script is invoked.
    """
    comp_dir = Path(staging_dir) / name
    spec_path = comp_dir / spec_filename

    # rpmautospec: expand %autorelease / %autochangelog in-place.
    rpa_result = subprocess.run(
        ["rpmautospec", "process-distgit", str(spec_path), str(spec_path)],
        capture_output=True,
        text=True,
        check=False,
    )

    if rpa_result.returncode != 0:
        return {
            "name": name,
            "specFiles": "",
            "error": f"rpmautospec failed: {rpa_result.stderr.strip()}",
        }

    # spectool: list source and patch files.
    st_result = subprocess.run(
        [
            "spectool",
            "--define",
            f"_sourcedir {comp_dir}",
            "-l",
            "-a",
            str(spec_path),
        ],
        capture_output=True,
        text=True,
        check=False,
    )

    if st_result.returncode != 0:
        return {
            "name": name,
            "specFiles": "",
            "error": f"spectool failed: {st_result.stderr.strip()}",
        }

    return {"name": name, "specFiles": st_result.stdout.strip(), "error": None}


def main() -> int:
    """Read inputs.json, process every component in parallel, and write results.json."""
    if len(sys.argv) != EXPECTED_ARG_COUNT:
        print(f"usage: {sys.argv[0]} <staging_dir> <max_workers>", file=sys.stderr)
        return 1

    staging_dir = sys.argv[1]
    max_workers = int(sys.argv[2])
    inputs_path = Path(staging_dir) / "inputs.json"

    with inputs_path.open() as f:
        inputs = json.load(f)

    # Mark all paths as git-safe (ownership mismatch between host and chroot).
    # safe.directory=* is acceptable here because this is an ephemeral, isolated
    # mock chroot that is destroyed after use.
    subprocess.run(
        ["git", "config", "--global", "--add", "safe.directory", "*"],
        check=False,
    )

    total = len(inputs)

    with ThreadPoolExecutor(max_workers=max_workers) as pool:
        futures = {
            pool.submit(
                process_component,
                staging_dir,
                comp["name"],
                comp["specFilename"],
            ): comp["name"]
            for comp in inputs
        }

        # Report progress to stderr as each component completes.
        # Note: mock --chroot merges the inner command's stderr into stdout,
        # so the Go caller uses SetRealTimeStdoutListener to receive these.
        completed_results = {}
        for idx, future in enumerate(as_completed(futures), 1):
            name = futures[future]
            try:
                completed_results[name] = future.result()
            except Exception as exc:  # noqa: BLE001 - record any worker failure as a per-component error
                completed_results[name] = {
                    "name": name,
                    "specFiles": "",
                    "error": str(exc),
                }

            print(f"PROGRESS {idx}/{total} {name}", file=sys.stderr, flush=True)

    # Collect results in input order (as_completed returns in completion order).
    results = [completed_results[comp["name"]] for comp in inputs]

    # Write results to a file in the staging directory rather than stdout.
    # This avoids bufio.Scanner token size limits in the Go caller, which
    # would truncate large JSON payloads (e.g., 7k components ≈ 560KB).
    results_path = Path(staging_dir) / "results.json"

    with results_path.open("w") as results_file:
        json.dump(results, results_file)

    return 0


if __name__ == "__main__":
    sys.exit(main())
