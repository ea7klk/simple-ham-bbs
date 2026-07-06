#!/usr/bin/env bash
set -euo pipefail

if [ -f /etc/bbs/bbs.env ]; then
  set -a
  # shellcheck disable=SC1091
  . /etc/bbs/bbs.env
  set +a
fi

exec /usr/local/bin/bbs_app.py
