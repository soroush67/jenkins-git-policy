# Lab stand-in for the GitLab Docker host: sshd + git-policy-ctl + docker CLI.
# Jenkins can only reach git-policy-ctl (forced command); gitpolicy-deploy has
# no shell access and no docker group, only the sudo rule for git-policy-ctl.
FROM alpine:3.20
RUN apk add --no-cache openssh-server bash sudo docker-cli coreutils \
 && adduser -D -s /bin/sh gitpolicy-deploy \
 && sed -i 's/^gitpolicy-deploy:!/gitpolicy-deploy:*/' /etc/shadow \
 && printf '%s\n' \
    'Defaults!/usr/local/sbin/git-policy-ctl env_keep += "SSH_ORIGINAL_COMMAND SSH_CLIENT"' \
    'gitpolicy-deploy ALL=(root) NOPASSWD: /usr/local/sbin/git-policy-ctl' > /etc/sudoers.d/git-policy \
 && chmod 0440 /etc/sudoers.d/git-policy \
 && printf '%s\n' \
    'PasswordAuthentication no' 'KbdInteractiveAuthentication no' 'PermitRootLogin no' \
    'AllowUsers gitpolicy-deploy' 'AllowTcpForwarding no' 'X11Forwarding no' 'PermitTunnel no' \
    'AuthorizedKeysFile /etc/ssh/authorized_keys/%u' > /etc/ssh/sshd_config.d/git-policy.conf
COPY git-policy-ctl /usr/local/sbin/git-policy-ctl
COPY entrypoint.sh /entrypoint.sh
RUN chmod 0755 /usr/local/sbin/git-policy-ctl /entrypoint.sh
EXPOSE 22
ENTRYPOINT ["/entrypoint.sh"]
