# A "machine" for the lab: systemd as PID 1 plus sshd, so Ansible can manage it
# exactly like a VM. See docs/adr/0001-systemd-containers-as-nodes.md.
FROM debian:12

ENV container=docker DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      systemd systemd-sysv dbus openssh-server sudo python3 python3-apt \
      ca-certificates curl gnupg iproute2 procps less \
 && apt-get clean && rm -rf /var/lib/apt/lists/* \
 && rm -f /lib/systemd/system/multi-user.target.wants/getty* \
 && systemctl enable ssh \
 && mkdir -p /root/.ssh && chmod 700 /root/.ssh \
 && sed -ri 's/^#?PermitRootLogin .*/PermitRootLogin prohibit-password/; s/^#?PasswordAuthentication .*/PasswordAuthentication no/' /etc/ssh/sshd_config

# Host keys are baked into the image and therefore shared by all nodes; acceptable
# for a local lab where Ansible disables host key checking.

STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
