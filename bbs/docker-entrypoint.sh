#!/usr/bin/env bash
set -euo pipefail

: "${BBS_USER_PASSWORD:=change-this-password}"
: "${BBS_GUEST_MODE:=false}"

mkdir -p /etc/bbs
{
  for name in BBS_NAME BBS_SYSOP BBS_SYSOPS BBS_LOCATION BBS_WELCOME_TOPIC BBS_DATA_DIR BBS_DB_FILE BBS_TRANSLATIONS_FILE APRS_IS_SERVER APRS_IS_PORT APRSD_BIN APRS_RECEIVER_CALLSIGN BBS_FALLBACK_GATEWAY; do
    printf '%s=%q\n' "$name" "${!name:-}"
  done
} > /etc/bbs/bbs.env
chmod 644 /etc/bbs/bbs.env

if [ "${BBS_GUEST_MODE}" = "true" ]; then
  passwd -d bbs >/dev/null
else
  echo "bbs:${BBS_USER_PASSWORD}" | chpasswd
fi

data_dir="${BBS_DATA_DIR:-/var/lib/bbs}"
host_key_dir="/config/ssh/host_keys"
mkdir -p /run/sshd "$data_dir/aprs" /home/bbs/.ssh "$host_key_dir"
touch "$data_dir/aprs/aprsd.log"
touch "$data_dir/aprs/receiver.log"
chown -R bbs:bbs /home/bbs/.ssh
chown -R bbs:bbs "$data_dir" || echo "Warning: could not change ownership of $data_dir"
chmod 700 /home/bbs/.ssh
rm -f "$data_dir/aprs/sent.json"

if [ ! -s "$host_key_dir/ssh_host_rsa_key" ]; then
  ssh-keygen -q -t rsa -b 4096 -f "$host_key_dir/ssh_host_rsa_key" -N ""
fi
if [ ! -s "$host_key_dir/ssh_host_ecdsa_key" ]; then
  ssh-keygen -q -t ecdsa -f "$host_key_dir/ssh_host_ecdsa_key" -N ""
fi
if [ ! -s "$host_key_dir/ssh_host_ed25519_key" ]; then
  ssh-keygen -q -t ed25519 -f "$host_key_dir/ssh_host_ed25519_key" -N ""
fi
chmod 600 "$host_key_dir"/ssh_host_*_key 2>/dev/null || true
chmod 644 "$host_key_dir"/ssh_host_*_key.pub 2>/dev/null || true
echo "Using persistent SSH host keys from $host_key_dir."

if command -v ip >/dev/null 2>&1 && ! ip route show default | grep -q '^default '; then
  if ip route add default via "${BBS_FALLBACK_GATEWAY:-172.18.0.1}" dev eth0; then
    echo "Added fallback Docker default route for outbound services."
  else
    echo "Warning: could not add fallback Docker default route." >&2
  fi
fi

(
  while true; do
    /usr/local/bin/bbs_app aprs-receiver
    echo "$(date -u '+%Y-%m-%d %H:%M UTC') APRS receiver exited; restarting in 10 seconds." >> "$data_dir/aprs/receiver.log"
    sleep 10
  done
) &

tail -n 0 -f "$data_dir/aprs/aprsd.log" "$data_dir/aprs/receiver.log" &

if [ -f /config/ssh/authorized_keys ]; then
  cp /config/ssh/authorized_keys /home/bbs/.ssh/authorized_keys
  chown bbs:bbs /home/bbs/.ssh/authorized_keys
  chmod 600 /home/bbs/.ssh/authorized_keys
fi

exec "$@"
