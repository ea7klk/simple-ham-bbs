#!/usr/bin/env bash
set -euo pipefail

: "${BBS_USER_PASSWORD:=change-this-password}"
: "${BBS_GUEST_MODE:=false}"

mkdir -p /etc/bbs
{
  for name in BBS_NAME BBS_SYSOP BBS_SYSOPS BBS_LOCATION BBS_WELCOME_TOPIC BBS_DATA_DIR BBS_TRANSLATIONS_FILE; do
    printf '%s=%q\n' "$name" "${!name:-}"
  done
} > /etc/bbs/bbs.env
chmod 644 /etc/bbs/bbs.env

if [ "${BBS_GUEST_MODE}" = "true" ]; then
  passwd -d bbs >/dev/null
else
  echo "bbs:${BBS_USER_PASSWORD}" | chpasswd
fi

mkdir -p /run/sshd /var/lib/bbs /home/bbs/.ssh
chown -R bbs:bbs /home/bbs/.ssh
chown -R bbs:bbs /var/lib/bbs || echo "Warning: could not change ownership of /var/lib/bbs"
chmod 700 /home/bbs/.ssh

if [ -f /config/ssh/authorized_keys ]; then
  cp /config/ssh/authorized_keys /home/bbs/.ssh/authorized_keys
  chown bbs:bbs /home/bbs/.ssh/authorized_keys
  chmod 600 /home/bbs/.ssh/authorized_keys
fi

ssh-keygen -A

exec "$@"
