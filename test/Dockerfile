# Dockerized install + end-to-end test for pssh.
# Build from the repo root:  docker build -f test/Dockerfile -t pssh-test .
# Run:                       docker run --rm pssh-test
FROM golang:1.26-bookworm

# Runtime deps: a real sshd/ssh client to exercise the askpass login flow.
RUN apt-get update && apt-get install -y --no-install-recommends \
        openssh-server openssh-client ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Test user whose unix password matches the fake passbolt resource's password.
ARG TEST_USER=tester
ARG TEST_PASS=Pa55w0rd-x9
RUN useradd -m -s /bin/bash "$TEST_USER" \
    && printf '%s:%s\n' "$TEST_USER" "$TEST_PASS" | chpasswd \
    && mkdir -p /run/sshd \
    && sed -ri 's/^#?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config \
    && sed -ri 's/^#?PermitRootLogin.*/PermitRootLogin no/'                /etc/ssh/sshd_config \
    && sed -ri 's/^#?UsePAM.*/UsePAM yes/'                                 /etc/ssh/sshd_config
ENV FAKE_PB_PASSWORD=${TEST_PASS}

# Fake passbolt CLI on PATH (no real Passbolt server needed).
COPY test/fake-passbolt /usr/local/bin/passbolt
RUN chmod +x /usr/local/bin/passbolt

# Build pssh from source.
WORKDIR /src
COPY . .
RUN go build -o /usr/local/bin/pssh .

COPY test/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
