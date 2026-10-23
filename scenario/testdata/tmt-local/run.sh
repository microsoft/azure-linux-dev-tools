#!/bin/bash
set -euo pipefail

cd /workdir/project
find /workdir/fixture /workdir/root-fixture -type f -print0 | sort -z | xargs -0 sha256sum >/artifacts/source-before
if rpm -q jq >/dev/null 2>&1; then
    echo "jq must be absent before TMT installs the declared dependency" >&2
    exit 1
fi

for format in json table; do
    azldev --no-default-config --dry-run component test azldev-tmt-probe \
        --provision local --source-dir /workdir/fixture --work-dir /artifacts/dry-run \
        --plan /plans/pass --rpm /opt/azldev-tmt-fixtures/candidate.rpm -O "${format}" \
        >"/artifacts/dry-${format}.out" 2>"/artifacts/dry-${format}.stderr"
    grep -q 'Would run' "/artifacts/dry-${format}.stderr"
    test ! -e /artifacts/dry-run
done

for case in pass private-data mixed skip fail warn with.dots empty prepare-failure finish-failure report-failure multi pass-empty root; do
    source=/workdir/fixture
    plan="/plans/${case}$"
    case "${case}" in
        multi) plan='/plans/(pass|skip)$' ;;
        pass-empty) plan='/plans/(pass|empty)$' ;;
        root) source=/workdir/root-fixture; plan='/$' ;;
    esac
    set +e
    TMT_STATE_FORMAT=json azldev --no-default-config component test azldev-tmt-probe \
        --provision local --source-dir "${source}" --work-dir /artifacts/work \
        --plan "${plan}" --rpm /opt/azldev-tmt-fixtures/candidate.rpm -O json \
        >"/artifacts/${case}.json" 2>"/artifacts/${case}.stderr"
    status=$?
    set -e
    printf '%s\n' "${status}" >"/artifacts/${case}.exit"
done

# A surviving descendant holds both capture pipes after its parent exits or is killed.
python=/artifacts/work/tmt/venv/bin/python3
mv /artifacts/work/tmt/venv/bin/tmt /artifacts/work/tmt/venv/bin/tmt-original
cat >/artifacts/work/tmt/venv/bin/tmt <<'SH'
#!/bin/bash
set -eu
while [[ "$1" != --id ]]; do shift; done
exec /artifacts/work/tmt/venv/bin/python3 - "$2" <<'PY'
import os
import subprocess
import sys
import time
from pathlib import Path

native = Path(sys.argv[1])
native.mkdir(parents=True)
(native / "diagnostic.txt").write_text("persisted evidence")
print("runner stdout", flush=True)
print("runner stderr", file=sys.stderr, flush=True)
child = subprocess.Popen([sys.executable, "-c", "import time; time.sleep(60)"])
Path(os.environ["AZL_TMT_READY"]).write_text(str(child.pid))
if not os.environ.get("AZL_TMT_EXIT"):
    time.sleep(60)
PY
SH
chmod 0755 /artifacts/work/tmt/venv/bin/tmt
"${python}" - <<'PY'
import os
import signal
import subprocess
import time
from pathlib import Path

for name, sig in (("sigint", signal.SIGINT), ("sigterm", signal.SIGTERM), ("pipe-timeout", None)):
    ready = Path(f"/artifacts/{name}.ready")
    with Path(f"/artifacts/{name}.json").open("w") as stdout, Path(f"/artifacts/{name}.stderr").open("w") as stderr:
        process = subprocess.Popen(
            ["azldev", "--no-default-config", "component", "test", "azldev-tmt-probe",
             "--provision", "local", "--source-dir", "/workdir/fixture",
             "--work-dir", "/artifacts/work", "--plan", "/plans/pass",
             "--rpm", "/opt/azldev-tmt-fixtures/candidate.rpm", "-O", "json"],
            stdout=stdout, stderr=stderr,
            env={**os.environ, "AZL_TMT_READY": str(ready), "AZL_TMT_EXIT": "1" if sig is None else ""},
        )
        try:
            deadline = time.monotonic() + 30
            while not ready.exists():
                if process.poll() is not None or time.monotonic() >= deadline:
                    raise RuntimeError(f"{name}: runner did not become ready")
                time.sleep(0.05)
            if sig is not None:
                process.send_signal(sig)
            status = process.wait(timeout=10)
            Path(f"/artifacts/{name}.exit").write_text(f"{status}\n")
        finally:
            if process.poll() is None:
                process.kill()
                process.wait(timeout=10)
            if ready.exists():
                os.kill(int(ready.read_text()), signal.SIGKILL)
PY
mv /artifacts/work/tmt/venv/bin/tmt-original /artifacts/work/tmt/venv/bin/tmt

find /workdir/fixture /workdir/root-fixture -type f -print0 | sort -z | xargs -0 sha256sum >/artifacts/source-after
cmp /artifacts/source-before /artifacts/source-after
rpm -q jq
