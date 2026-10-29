#!/bin/bash
set -euo pipefail
export TMT_SSH_STRICT_HOST_KEY_CHECKING=yes
export TMT_SSH_USER_KNOWN_HOSTS_FILE=/home/testuser/.ssh/known_hosts

sudo chown -R "$(id -u):$(id -g)" /home/testuser/.ssh
chmod 0700 /home/testuser/.ssh
chmod 0600 /home/testuser/.ssh/id_ed25519
ssh_args=(-i /home/testuser/.ssh/id_ed25519 -p 2222 -o BatchMode=yes -o StrictHostKeyChecking=yes)
guest=tester@guest
ssh "${ssh_args[@]}" "$guest" \
    'set -e
     test "$(id -u)" != 0
     sudo -n true
     if rpm -q jq; then exit 1; fi
     if rpm -q azldev-tmt-probe; then exit 1; fi
     test ! -e /workdir
     test ! -e /home/tester/executed'

# The remote work root must be creatable by the unprivileged SSH user.
work=/var/tmp/azldev-connect
trap 'for dir in "$work"/azldev-test-*; do [ ! -d "$dir" ] || cp -a "$dir" /artifacts/; done' EXIT
ssh "${ssh_args[@]}" "$guest" "mkdir -p $work; test -w $work"
mkdir -p "$work"

run_case() {
    local name=$1 plan=$2
    shift 2
    set +e
    env -C /workdir/project TMT_STATE_FORMAT=json azldev --no-default-config \
        component test azldev-tmt-probe --provision connect \
        --guest guest --port 2222 --user tester --key /home/testuser/.ssh/id_ed25519 \
        --source-dir /workdir/fixture --work-dir "$work" --plan "$plan" -O json "$@" \
        >"/artifacts/$name.json" 2>"/artifacts/$name.stderr"
    status=$?
    set -e
    printf '%s\n' "$status" >"/artifacts/$name.exit"
}

# Trust is deliberately absent for the first invocation. No test may execute.
mv /home/testuser/.ssh/known_hosts /home/testuser/.ssh/trusted_hosts
touch /home/testuser/.ssh/known_hosts
run_case unknown /plans/pass
mv /home/testuser/.ssh/trusted_hosts /home/testuser/.ssh/known_hosts
ssh "${ssh_args[@]}" "$guest" 'set -e; test ! -e /home/tester/executed; ! rpm -q jq'

run_case pass /plans/pass
ssh "${ssh_args[@]}" "$guest" \
    'set -e; rpm -q jq; if rpm -q azldev-tmt-probe; then exit 1; fi; test "$(cat /home/tester/executed)" = pass'
run_case mixed /plans/mixed --rpm /opt/azldev-tmt-fixtures/candidate.rpm
expected=$(rpm -qp --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' /opt/azldev-tmt-fixtures/candidate.rpm)
ssh "${ssh_args[@]}" "$guest" 'rpm -q azldev-tmt-probe; cat /home/tester/executed' >/artifacts/guest-state
grep -Fx "$expected" /artifacts/guest-state
test "$(grep -c '^pass$' /artifacts/guest-state)" = 2
test "$(grep -c '^fail$' /artifacts/guest-state)" = 1
test "$(grep -c '^error$' /artifacts/guest-state)" = 1
