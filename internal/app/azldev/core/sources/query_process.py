#!/usr/bin/env python3
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

"""Query RPM specs inside a mock chroot: run rpmspec twice per component (once
with --srpm for source NEVR, once without for binary subpackage names) and
write per-component results to a JSON file in the scratch directory.

This script is embedded in the azldev Go binary and executed inside a mock chroot
during ``azldev component query``. It mirrors render_process.py's shape (a
ThreadPoolExecutor over per-component work, PROGRESS lines on stderr, a
results.json file in the scratch dir) so the Go-side plumbing can be shared.

Usage::

    python3 query_process.py <scratch_dir> <specs_dir> <max_workers>

The scratch directory must contain an ``inputs.json`` file::

    [
      {
        "name": "curl",
        "specRelPath": "c/curl/curl.spec",
        "srpmQueryFormat": "name=%{name}\\n...",
        "subpackagesQueryFormat": "subpkg=%{name}\\n",
        "with": ["foo"],
        "without": ["bar"],
        "defines": {"_sourcedir": "/some/path"}
      },
      ...
    ]

Results are written to ``<scratch_dir>/results.json``::

    [
      {"name": "curl", "srpmOut": "name=curl\\n...", "binOut": "subpkg=curl\\n...", "error": null},
      {"name": "broken", "srpmOut": "", "binOut": "", "error": "rpmspec --srpm failed: ..."}
    ]

Progress is reported to stderr as ``PROGRESS <completed>/<total> <name>``.
"""

import json
import os
import subprocess
import sys
from concurrent.futures import ThreadPoolExecutor, as_completed


def _rpmspec_args(spec_path, query_format, srpm, with_, without, defines, arch):
    """Compose an rpmspec command line.

    Always overrides _sourcedir and _specdir to the spec's own directory so
    that sidecar files (e.g. `Source1: foo.azl.macros`) loaded with
    `%{SOURCEN}` or `%{load:...}` resolve against the rendered spec tree
    rather than mock's default /builddir/build/SOURCES. Also sets
    `with_check 0` to match the legacy per-component rpmspec path.

    `_ghc_version_cache` short-circuits `%ghc_version` in ghc-rpm-macros,
    which would otherwise run `ghc --numeric-version`. We don't install the
    ghc compiler in the query chroot, so the lookup would fail with
    "command not found", producing parse errors like:
        error: line N: Version required: Requires:  ghc-compiler =
    We set `_ghc_version_cache` rather than the higher-priority
    `ghc_version_override` because some specs (notably ghc.spec itself)
    redefine `ghc_version_override` via `%global`; command-line -D macros
    are sticky and would block those overrides. `_ghc_version_cache` is
    consulted after `ghc_version_override` inside the macro, so any spec
    setting the latter still wins, and we only intercept the shell-out
    path that's broken for us. The exact value only feeds Requires/Provides
    version tags; subpackage names don't depend on it, so a placeholder is
    fine for our purpose.

    `arch`, when non-empty, is passed as --target=<arch>. This drives the
    %_target_cpu macro family inside rpmspec so ExclusiveArch/ExcludeArch
    checks and arch-conditional %ifarch blocks evaluate for the requested
    target rather than the host arch.

    User-provided defines win on the rpmspec side (rpmspec honors the last
    -D for a given macro), so we list ours first.
    """
    spec_dir = os.path.dirname(spec_path)
    args = ["rpmspec", "-q"]
    if srpm:
        args.append("--srpm")
    if arch:
        args.append(f"--target={arch}")
    args += ["--queryformat", query_format]
    args += ["-D", f"_sourcedir {spec_dir}"]
    args += ["-D", f"_specdir {spec_dir}"]
    args += ["-D", "with_check 0"]
    args += ["-D", "_ghc_version_cache 0.0.0"]
    for w in with_:
        args += ["--with", w]
    for w in without:
        args += ["--without", w]
    for key, value in defines.items():
        args += ["-D", f"{key} {value}"]
    args.append(spec_path)
    return args


# Per-spec rewrites that work around quirks no -D override can fix.
#
# Each entry maps a spec basename to a list of (find, replace) tuples
# applied to the spec text before rpmspec is invoked. The rewrite happens
# on a scratch copy in the scratch dir; the original file in the rendered
# specs tree is never modified.
_SPEC_REWRITES = {
    "ghc.spec": [
        # ghc.spec %undefines _ghcdynlibdir (line ~475) which defeats any
        # -D _ghcdynlibdir override. The %post/%postun scriptlets that
        # depend on it are then emitted inside `%if "%{?_ghcdynlibdir}" !=
        # "%_libdir"` and break rpmspec parsing with "package ghc-base does
        # not exist" when ghc-rpm-macros is loaded but the ghc compiler
        # isn't installed in our query chroot. We comment these scriptlets
        # out — they don't affect subpackage enumeration.
        ("%post base -p /sbin/ldconfig", "# patched-out-for-azldev-query: %post base"),
        ("%postun base -p /sbin/ldconfig", "# patched-out-for-azldev-query: %postun base"),
    ],
}


def _maybe_rewrite_spec(spec_path, scratch_dir, comp_name):
    """If spec_path needs known patches to parse under rpmspec, write a
    rewritten copy into scratch_dir and return its path. Otherwise return
    spec_path unchanged.
    """
    rewrites = _SPEC_REWRITES.get(os.path.basename(spec_path))
    if not rewrites:
        return spec_path

    with open(spec_path, encoding="utf-8", errors="replace") as src:
        content = src.read()

    for find, replace in rewrites:
        content = content.replace(find, replace)

    out_path = os.path.join(scratch_dir, f"{comp_name}.patched.spec")
    with open(out_path, "w", encoding="utf-8") as dst:
        dst.write(content)

    return out_path


# Per-invocation timeout for rpmspec, in seconds. rpmspec on a healthy spec
# completes in well under a second; this generous cap exists only to bound
# pathological cases (recursive macros, macros that shell out and block) so
# one wedged spec can't hang the whole batch.
_RPMSPEC_TIMEOUT_SECONDS = 180


class _RpmspecTimeout(Exception):
    """Raised when rpmspec exceeds _RPMSPEC_TIMEOUT_SECONDS."""


def _run_rpmspec(args):
    """Run rpmspec and return (stdout, stderr, returncode).

    Raises _RpmspecTimeout if rpmspec doesn't finish within
    _RPMSPEC_TIMEOUT_SECONDS. On timeout, the child process is killed before
    re-raising so it doesn't linger inside the mock chroot.
    """
    try:
        proc = subprocess.run(
            args,
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=_RPMSPEC_TIMEOUT_SECONDS,
        )
    except subprocess.TimeoutExpired as exc:
        # subprocess.run already terminated the child by the time TimeoutExpired
        # is raised, but stdout/stderr captured up to the timeout are on the
        # exception.
        stdout = exc.stdout or ""
        stderr = exc.stderr or ""
        if isinstance(stdout, bytes):
            stdout = stdout.decode("utf-8", errors="replace")
        if isinstance(stderr, bytes):
            stderr = stderr.decode("utf-8", errors="replace")
        raise _RpmspecTimeout(
            f"rpmspec timed out after {_RPMSPEC_TIMEOUT_SECONDS}s; "
            f"last stderr: {stderr.strip()[-512:]}"
        ) from exc
    return proc.stdout, proc.stderr, proc.returncode


# rpmspec (unlike rpmbuild) does NOT enforce ExclusiveArch/ExcludeArch on
# its own: both --srpm and --builtrpms queries return rc=0 against a spec
# whose ExclusiveArch excludes the --target arch. To honor those tags we
# read them out of the spec via an extra block wrapped into the srpm
# queryformat and evaluate the policy ourselves before running the binary
# phase. The wrapper uses sentinel lines so we can split the probe data
# back out and hand the caller-supplied portion of srpmOut through clean.
#
# `[%{Tag} ]` queryformat lists each value separated by a space; an empty
# tag yields an empty string, so absent ExclusiveArch/ExcludeArch parses
# as an empty list (== no restriction).
_ARCH_PROBE_BEGIN = "__AZL_ARCH_PROBE_BEGIN__\n"
_ARCH_PROBE_END = "__AZL_ARCH_PROBE_END__\n"
_ARCH_PROBE_FORMAT = (
    _ARCH_PROBE_BEGIN
    + "EA=[%{ExclusiveArch} ]\n"
    + "XA=[%{ExcludeArch} ]\n"
    + _ARCH_PROBE_END
)


def _wrap_srpm_format_with_arch_probe(query_format):
    """Prepend the arch-probe block to the caller's srpm queryformat."""
    return _ARCH_PROBE_FORMAT + query_format


def _split_arch_probe(srpm_out):
    """Extract (exclusive_arch_list, exclude_arch_list, cleaned_srpm_out).

    If the probe markers are absent (older callers, malformed output) the
    arch lists are empty and srpm_out is returned unchanged. Lowercase the
    arch tokens because rpm normalizes arch names that way and our target
    arch (qemu.Arch) is always lowercase.
    """
    start = srpm_out.find(_ARCH_PROBE_BEGIN)
    end = srpm_out.find(_ARCH_PROBE_END)
    if start < 0 or end < 0 or end < start:
        return [], [], srpm_out
    probe = srpm_out[start + len(_ARCH_PROBE_BEGIN):end]
    cleaned = srpm_out[:start] + srpm_out[end + len(_ARCH_PROBE_END):]
    ea, xa = [], []
    for line in probe.splitlines():
        if line.startswith("EA="):
            ea = line[len("EA="):].lower().split()
        elif line.startswith("XA="):
            xa = line[len("XA="):].lower().split()
    return ea, xa, cleaned


# rpm canonicalizes a handful of arch aliases before comparing against
# ExclusiveArch/ExcludeArch. Mirror just the pairs that matter for arches
# azldev supports today.
_ARCH_ALIASES = {
    "amd64": "x86_64",
    "arm64": "aarch64",
}


def _canonicalize_arch_token(token):
    return _ARCH_ALIASES.get(token, token)


def _is_arch_excluded(arch, exclusive_arch, exclude_arch):
    """Return True iff target arch is excluded by ExclusiveArch/ExcludeArch.

    `noarch` in ExclusiveArch means "any arch" and never excludes. With an
    empty target arch (caller opted out of arch filtering) we never
    exclude.

    Spec tokens and the target arch are canonicalized through a small alias
    map first (amd64 -> x86_64, arm64 -> aarch64) so specs that spell their
    arches the Debian/Ubuntu way aren't silently dropped. We mirror only
    the pairs that matter for arches azldev supports
    (qemu.SupportedArchitectures: x86_64, aarch64); the ix86 family and
    other rpm aliases are intentionally not modeled.
    """
    if not arch:
        return False
    arch = _canonicalize_arch_token(arch.lower())
    exclusive_arch = [_canonicalize_arch_token(a) for a in exclusive_arch]
    exclude_arch = [_canonicalize_arch_token(a) for a in exclude_arch]
    if exclusive_arch and "noarch" not in exclusive_arch and arch not in exclusive_arch:
        return True
    if arch in exclude_arch:
        return True
    return False


def process_component(specs_dir, scratch_dir, comp, arch):
    """Run rpmspec --srpm + rpmspec (no --srpm) for one component.

    Trust boundary: comp["name"] and comp["specRelPath"] are validated by
    BatchQuerySpecs in mockprocessor.go before this script is invoked.
    arch is a target arch (e.g. "x86_64"); when non-empty it is passed to
    rpmspec via --target. Specs that ExclusiveArch/ExcludeArch-exclude the
    target are returned with excludedFromArch=True (not an error).
    """
    name = comp["name"]
    spec_path = os.path.join(specs_dir, comp["specRelPath"])
    with_ = comp.get("with", []) or []
    without = comp.get("without", []) or []
    defines = comp.get("defines", {}) or {}

    if not os.path.isfile(spec_path):
        return {
            "name": name,
            "srpmOut": "",
            "binOut": "",
            "error": f"spec file not found: {comp['specRelPath']}",
        }

    # Apply per-spec rewrites (e.g. ghc.spec) to a scratch copy if needed.
    # _sourcedir/_specdir stay pinned to the original spec's directory via
    # _rpmspec_args, so sidecar files still resolve correctly.
    effective_spec = _maybe_rewrite_spec(spec_path, scratch_dir, name)

    # Source-level query (--srpm). The caller's srpmQueryFormat is wrapped
    # with an arch-policy probe block (see _wrap_srpm_format_with_arch_probe);
    # we split that probe back out before returning srpm_out to Go so the
    # downstream parser only sees the caller-requested fields.
    srpm_args = _rpmspec_args(
        effective_spec,
        _wrap_srpm_format_with_arch_probe(comp["srpmQueryFormat"]),
        True,
        with_,
        without,
        defines,
        arch,
    )
    try:
        srpm_out, srpm_err, srpm_rc = _run_rpmspec(srpm_args)
    except _RpmspecTimeout as exc:
        return {
            "name": name,
            "srpmOut": "",
            "binOut": "",
            "error": f"rpmspec --srpm {exc}",
        }
    if srpm_rc != 0:
        return {
            "name": name,
            "srpmOut": srpm_out,
            "binOut": "",
            "error": f"rpmspec --srpm failed: {srpm_err.strip()}",
        }

    exclusive_arch, exclude_arch, srpm_out = _split_arch_probe(srpm_out)
    if _is_arch_excluded(arch, exclusive_arch, exclude_arch):
        return {
            "name": name,
            "srpmOut": srpm_out,
            "binOut": "",
            "error": None,
            "excludedFromArch": True,
        }

    # Binary subpackage enumeration (no --srpm).
    #
    # `--builtrpms` (vs the default `--rpms`) restricts the listing to binary
    # packages that *would actually be built*, i.e. those with a `%files`
    # section. This matters for specs like `wayland` whose main package has
    # no `%files` and produces no binary RPM — only its subpackages
    # (libwayland-client, etc.) do. Using `--builtrpms` makes the output a
    # ground-truth list of the binary RPMs the spec would produce.
    bin_args = _rpmspec_args(
        effective_spec,
        comp["subpackagesQueryFormat"],
        False,
        with_,
        without,
        defines,
        arch,
    )
    # Insert --builtrpms right after `-q` so it associates with the query.
    bin_args.insert(2, "--builtrpms")
    try:
        bin_out, bin_err, bin_rc = _run_rpmspec(bin_args)
    except _RpmspecTimeout as exc:
        return {
            "name": name,
            "srpmOut": srpm_out,
            "binOut": "",
            "error": f"rpmspec (binary) {exc}",
        }
    if bin_rc != 0:
        return {
            "name": name,
            "srpmOut": srpm_out,
            "binOut": bin_out,
            "error": f"rpmspec failed: {bin_err.strip()}",
        }

    return {
        "name": name,
        "srpmOut": srpm_out,
        "binOut": bin_out,
        "error": None,
    }


def main() -> int:
    if len(sys.argv) != 5:
        print(
            f"usage: {sys.argv[0]} <scratch_dir> <specs_dir> <max_workers> <arch>",
            file=sys.stderr,
        )
        return 1

    scratch_dir = sys.argv[1]
    specs_dir = sys.argv[2]
    max_workers = int(sys.argv[3])
    arch = sys.argv[4]
    inputs_path = os.path.join(scratch_dir, "inputs.json")

    with open(inputs_path) as f:
        inputs = json.load(f)

    total = len(inputs)

    with ThreadPoolExecutor(max_workers=max_workers) as pool:
        futures = {
            pool.submit(process_component, specs_dir, scratch_dir, comp, arch): comp["name"]
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
            except Exception as exc:
                completed_results[name] = {
                    "name": name,
                    "srpmOut": "",
                    "binOut": "",
                    "error": str(exc),
                }

            print(f"PROGRESS {idx}/{total} {name}", file=sys.stderr, flush=True)

    # Collect results in input order (as_completed returns in completion order).
    results = [completed_results[comp["name"]] for comp in inputs]

    results_path = os.path.join(scratch_dir, "results.json")
    with open(results_path, "w") as results_file:
        json.dump(results, results_file)

    return 0


if __name__ == "__main__":
    sys.exit(main())
