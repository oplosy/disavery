# Pinned operator tooling. Terraform, Ansible and the drill CLI all run here so
# every user and CI job gets identical versions (Ansible does not run on Windows).
FROM golang:1.26-bookworm

ARG TERRAFORM_VERSION=1.9.8
ARG SOPS_VERSION=3.9.1
ARG AGE_VERSION=1.2.0
ARG STEP_VERSION=0.27.4
ARG DOCKER_VERSION=27.3.1

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      python3 python3-venv openssh-client postgresql-client dnsutils jq unzip curl ca-certificates \
 && apt-get clean && rm -rf /var/lib/apt/lists/*

RUN curl -fsSLo /tmp/tf.zip "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_amd64.zip" \
 && unzip -q /tmp/tf.zip -d /usr/local/bin && rm /tmp/tf.zip \
 && curl -fsSLo /usr/local/bin/sops "https://github.com/getsops/sops/releases/download/v${SOPS_VERSION}/sops-v${SOPS_VERSION}.linux.amd64" \
 && chmod +x /usr/local/bin/sops \
 && curl -fsSL "https://github.com/FiloSottile/age/releases/download/v${AGE_VERSION}/age-v${AGE_VERSION}-linux-amd64.tar.gz" \
    | tar -xz -C /usr/local/bin --strip-components=1 age/age age/age-keygen \
 && curl -fsSL "https://github.com/smallstep/cli/releases/download/v${STEP_VERSION}/step_linux_${STEP_VERSION}_amd64.tar.gz" \
    | tar -xz -C /usr/local/bin --strip-components=2 "step_${STEP_VERSION}/bin/step" \
 && curl -fsSL "https://download.docker.com/linux/static/stable/x86_64/docker-${DOCKER_VERSION}.tgz" \
    | tar -xz -C /usr/local/bin --strip-components=1 docker/docker

# minio and mc come from the locally built image (make minio-image); Ansible
# copies minio from here to the object-storage nodes.
COPY --from=disavery/minio:local /usr/local/bin/minio /usr/local/bin/mc /usr/local/bin/

COPY requirements.txt requirements.yml /tmp/
RUN python3 -m venv /opt/ansible \
 && /opt/ansible/bin/pip install --no-cache-dir -r /tmp/requirements.txt \
 && /opt/ansible/bin/ansible-galaxy collection install -r /tmp/requirements.yml -p /usr/share/ansible/collections \
 && mkdir -p /root/.terraform.d/plugin-cache

ENV PATH=/opt/ansible/bin:$PATH

# The checkout is bind-mounted at /work and owned by the host user (uid 1001
# on CI runners); without this, git refuses it and `go build` cannot stamp
# the binaries with their commit.
RUN git config --system --add safe.directory /work
