#!/bin/sh
# Install root:root 0755 as /usr/local/sbin/exchange-production-cd.
# The deployment account may sudo only this wrapper, never python/bash directly.
set -eu
[ "$#" -eq 1 ] || exit 2
exec /usr/bin/env -i PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin HOME=/root \
  /usr/bin/python3 /usr/local/lib/exchange-platform-cd/server.py "$1"
