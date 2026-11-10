#!/bin/bash
set -euo pipefail

readonly work_root="${AZL_TMT_CONTAINER_WORK_ROOT}"
readonly artifact_dir="${work_root}/artifacts"
readonly tmt_work_dir="${work_root}/tmt-work"
readonly source_dir="/workdir/fixture"
readonly image_path="/inputs/vm-base.qcow2"
readonly rpm_path="${AZL_TMT_CONTAINER_RPM}"

mkdir -p "${artifact_dir}" "${tmt_work_dir}"
chmod 0700 "${work_root}" "${artifact_dir}"

# Session libvirt needs a user-owned runtime directory. The daemon and every
# QEMU child remain inside this disposable container, so container removal owns
# cleanup.
export XDG_RUNTIME_DIR="/tmp/azltmt-${AZL_TMT_RUN_ID}"
export LIBVIRT_DEFAULT_URI="qemu:///session"
mkdir -m 0700 "${XDG_RUNTIME_DIR}"

{
    echo "image-revision=${AZL_TMT_IMAGE_REVISION}"
    sha256sum "${image_path}"
    sha256sum "${rpm_path}"
} >"${artifact_dir}/inputs"
rpm -qp --qf '%|EPOCH?{%{EPOCH}}:{0}|:%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}\n' "${rpm_path}" |
    tee "${artifact_dir}/candidate-nevra" >"${source_dir}/tests/smoke/expected-nevra"

# Fail early only for prerequisites whose downstream errors are opaque.
[[ -r /dev/kvm && -w /dev/kvm ]] || {
    echo "/dev/kvm is not readable and writable by container user $(id)" >&2
    exit 20
}
command -v genisoimage >/dev/null
firmware="$(find /usr/share -type f \( -name 'OVMF_CODE*.fd' -o -name 'QEMU_EFI*.fd' \) -print -quit)"
[[ -n "${firmware}" ]] || {
    echo "UEFI firmware was not found" >&2
    exit 21
}

# Containers do not run a user systemd instance, so start session libvirt
# directly. Docker init forwards termination and reaps the daemon and QEMU.
libvirtd -d
virsh uri

cd "${source_dir}"
azldev component test azldev-tmt-probe \
    --provision virtual \
    --source-dir "${source_dir}" \
    --work-dir "${tmt_work_dir}" \
    --plan /plans/smoke \
    --image-path "${image_path}" \
    --rpm "${rpm_path}" \
    2>&1 | tee "${artifact_dir}/azldev.log"

venv_python="${tmt_work_dir}/tmt/venv/bin/python3"
"${venv_python}" -m pip freeze >"${artifact_dir}/python-freeze"
while IFS= read -r requirement; do
    [[ -n "${requirement}" && "${requirement}" != \#* ]] || continue
    grep -Fxi "${requirement}" "${artifact_dir}/python-freeze"
done <"/workdir/tmt-requirements.txt"

results_file="$(find "${tmt_work_dir}" -type f -name results.yaml -print -quit)"
[[ -n "${results_file}" ]] || {
    echo "TMT produced no results.yaml" >&2
    exit 30
}
grep -q '/tests/smoke' "${results_file}"
grep -q 'result: pass' "${results_file}"
