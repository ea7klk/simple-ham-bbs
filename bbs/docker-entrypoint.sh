#!/usr/bin/env bash
set -euo pipefail

: "${BBS_USER_PASSWORD:=change-this-password}"
: "${BBS_GUEST_MODE:=false}"

data_dir="${BBS_DATA_DIR:-/var/lib/bbs}"
host_key_dir="/config/ssh/host_keys"

write_bbs_env() {
  mkdir -p /etc/bbs
  {
    for name in BBS_NAME BBS_SYSOP BBS_SYSOPS BBS_LOCATION BBS_WELCOME_TOPIC BBS_DATA_DIR BBS_DB_FILE BBS_TRANSLATIONS_FILE APRS_IS_SERVER APRS_IS_PORT APRSD_BIN APRS_RECEIVER_CALLSIGN BBS_FALLBACK_GATEWAY; do
      printf '%s=%q\n' "$name" "${!name:-}"
    done
  } > /etc/bbs/bbs.env
  chmod 644 /etc/bbs/bbs.env
}

configure_login_password() {
  if [ "${BBS_GUEST_MODE}" = "true" ]; then
    passwd -d bbs >/dev/null
  else
    echo "bbs:${BBS_USER_PASSWORD}" | chpasswd
  fi
}

prepare_runtime_dirs() {
  mkdir -p /run/sshd "$data_dir/aprs" /home/bbs/.ssh "$host_key_dir"
  chown -R bbs:bbs /home/bbs/.ssh
  chown -R bbs:bbs "$data_dir" || echo "Warning: could not change ownership of $data_dir"
  chmod 700 /home/bbs/.ssh
}

ensure_host_key() {
  local type="$1"
  local bits="${2:-}"
  local path="$host_key_dir/ssh_host_${type}_key"

  if [ -s "$path" ]; then
    return
  fi
  if [ -n "$bits" ]; then
    ssh-keygen -q -t "$type" -b "$bits" -f "$path" -N ""
  else
    ssh-keygen -q -t "$type" -f "$path" -N ""
  fi
}

ensure_host_keys() {
  ensure_host_key rsa 4096
  ensure_host_key ecdsa
  ensure_host_key ed25519
  chmod 600 "$host_key_dir"/ssh_host_*_key 2>/dev/null || true
  chmod 644 "$host_key_dir"/ssh_host_*_key.pub 2>/dev/null || true
  echo "Using persistent SSH host keys from $host_key_dir."
}

configure_fallback_route() {
  if command -v ip >/dev/null 2>&1 && ! ip route show default | grep -q '^default '; then
    if ip route add default via "${BBS_FALLBACK_GATEWAY:-172.18.0.1}" dev eth0; then
      echo "Added fallback Docker default route for outbound services."
    else
      echo "Warning: could not add fallback Docker default route." >&2
    fi
  fi
}

install_authorized_keys() {
  if [ -f /config/ssh/authorized_keys ]; then
    cp /config/ssh/authorized_keys /home/bbs/.ssh/authorized_keys
    chown bbs:bbs /home/bbs/.ssh/authorized_keys
    chmod 600 /home/bbs/.ssh/authorized_keys
  fi
}

start_helper_processes() {
  (
    while true; do
      /usr/local/bin/bbs_app aprs-supervisor
      echo "$(date -u '+%Y-%m-%d %H:%M UTC') APRS supervisor exited; restarting in 10 seconds." >&2
      sleep 10
    done
  ) &
}

write_bbs_env
configure_login_password
prepare_runtime_dirs
ensure_host_keys
configure_fallback_route
install_authorized_keys
start_helper_processes

exec "$@"
