#!/bin/sh
set -e
ssh-keygen -A >/dev/null
# authorized_keys must be root-owned and not writable by the user (StrictModes).
mkdir -p /etc/ssh/authorized_keys
install -o root -g root -m 0644 /run/authorized_keys /etc/ssh/authorized_keys/gitpolicy-deploy
touch /var/log/git-policy-ctl.log && chmod 0600 /var/log/git-policy-ctl.log
exec /usr/sbin/sshd -D -e
