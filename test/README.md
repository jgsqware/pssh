# Dockerized install + e2e test

A self-contained integration test that exercises the full pssh flow in a clean
container — **no VPN, no real Passbolt server required**.

## Run

```sh
mise run test-docker
# or:
docker build -f test/Dockerfile -t pssh-test .
docker run --rm pssh-test
```

## What it does

1. **Builds** pssh from source (`golang:1.26-bookworm`).
2. **`go vet` + unit tests** (`go test ./...`).
3. **`pssh doctor`** for visibility (clipboard/ssh-config show as N/A under root — expected).
4. **End-to-end askpass login:** stands up a real `sshd` with password auth and a
   `tester` user, links a `Host testbox` to a fake resource via a `# pssh:`
   comment, then runs `pssh testbox echo PSSH_E2E_OK`. pssh fetches the password
   from the fake passbolt CLI and logs in through `SSH_ASKPASS` — asserting the
   remote command output.
5. **Negative check:** with the wrong password the login must fail, proving the
   password is genuinely driving authentication (not bypassed).

## Pieces

- `Dockerfile` — build + sshd + test user (`tester` / `Pa55w0rd-x9`).
- `fake-passbolt` — a stub `passbolt` CLI returning one canned resource whose
  password matches the test user's unix password. Honors `FAKE_PB_PASSWORD` /
  `FAKE_PB_ID`.
- `entrypoint.sh` — orchestrates the steps above and exits non-zero on any failure.
