#!/usr/bin/env bash
# Docker test entrypoint: unit tests + a real end-to-end askpass login against a
# local sshd, using the fake passbolt stub. Exits non-zero on any failure.
set -euo pipefail

pass() { printf '\033[32m✓ %s\033[0m\n' "$*"; }
fail() { printf '\033[31m✗ %s\033[0m\n' "$*"; exit 1; }

echo "== go vet + unit tests =="
cd /src
go vet ./...
go test ./...
pass "unit tests"

echo "== pssh doctor (informational; passbolt is the fake stub) =="
pssh doctor || true

echo "== start sshd =="
ssh-keygen -A >/dev/null
/usr/sbin/sshd
for _ in $(seq 1 20); do
  ssh-keyscan -p 22 127.0.0.1 >/dev/null 2>&1 && break
  sleep 0.25
done

echo "== prepare tester's ssh config (host linked via # pssh: comment) =="
install -d -o tester -g tester -m 700 /home/tester/.ssh
cat >/home/tester/.ssh/config <<EOF
Host testbox
    HostName 127.0.0.1
    User tester
    StrictHostKeyChecking no
    UserKnownHostsFile /dev/null
    # pssh: test-resource-id
EOF
chown tester:tester /home/tester/.ssh/config
chmod 600 /home/tester/.ssh/config

echo "== e2e: pssh resolves the secret and logs in via askpass =="
# Run as tester; pssh should fetch the (fake) password and exec ssh, which
# authenticates non-interactively through SSH_ASKPASS and runs the remote command.
out=$(runuser -u tester -- env PSSH_DELIVER=askpass PSSH_PASSBOLT_FLAGS= \
  pssh testbox echo PSSH_E2E_OK 2>/tmp/e2e.err) || {
  echo "--- stderr ---"; cat /tmp/e2e.err; fail "pssh e2e command failed"
}
echo "remote returned: ${out}"
case "$out" in
  *PSSH_E2E_OK*) pass "end-to-end askpass login" ;;
  *) echo "--- stderr ---"; cat /tmp/e2e.err; fail "unexpected remote output" ;;
esac

echo "== negative: wrong password must NOT authenticate =="
if runuser -u tester -- env PSSH_DELIVER=askpass FAKE_PB_PASSWORD=wrong-pw \
     pssh testbox echo SHOULD_NOT_HAPPEN >/dev/null 2>&1; then
  fail "login succeeded with the wrong password (askpass not actually used?)"
fi
pass "wrong password correctly rejected"

echo
pass "ALL DOCKER TESTS PASSED"
