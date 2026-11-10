#!/bin/bash
set -euo pipefail

source /etc/os-release
[[ "${ID}" == "azurelinux" ]]
[[ "${VERSION_ID}" == "4.0" || "${VERSION_ID}" == "4" ]]

[[ "$(id -u)" == "0" ]]
[[ "${SUDO_USER:-}" == "azureuser" ]]
systemctl is-active --quiet firewalld

sshd_policy="$(sshd -T -C user=root,host=localhost,addr=127.0.0.1)"
grep -qx "permitrootlogin no" <<<"${sshd_policy}"
grep -qx "passwordauthentication no" <<<"${sshd_policy}"

expected_nevra="$(<"$(dirname "$0")/expected-nevra")"
actual_nevra="$(rpm -q --qf '%|EPOCH?{%{EPOCH}}:{0}|:%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}\n' azldev-tmt-probe)"
[[ "${actual_nevra}" == "${expected_nevra}" ]]
[[ "$(rpm -qf /usr/bin/azldev-tmt-probe)" == azldev-tmt-probe-* ]]

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT
chown azureuser:azureuser "${tmpdir}"
printf '%s\n' "probe round trip" >"${tmpdir}/input"
chown azureuser:azureuser "${tmpdir}/input"
sudo -u azureuser -- azldev-tmt-probe "${tmpdir}/input" "${tmpdir}/output"
grep -qx "PROBE ROUND TRIP" "${tmpdir}/output"
