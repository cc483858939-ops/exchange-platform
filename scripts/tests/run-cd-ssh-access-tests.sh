#!/usr/bin/env bash
# Local/CI tests only: no SSH connection, listener, account creation or service install.
set -euo pipefail
[[ "$(id -u)" == 0 ]] || { printf 'Run this isolated test as root.\n' >&2; exit 1; }
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
test_tools="$(mktemp -d /tmp/exchange-cd-ssh-tests.XXXXXXXX)"
created_privsep_dir=0
cleanup() {
  if [[ "$created_privsep_dir" == 1 ]]; then rmdir /run/sshd 2>/dev/null || true; fi
  rm -rf -- "$test_tools"
}
trap cleanup EXIT
if [[ -x /usr/sbin/sshd ]]; then
  export CD_TEST_SSHD=/usr/sbin/sshd
else
  # Extract distro binaries only. dpkg -i / apt install would run maintainer
  # scripts and might start sshd, which these offline tests must never do.
  (
    cd "$test_tools"
    apt-get download openssh-server libwrap0 libnss-wrapper
    for package in ./*.deb; do dpkg-deb -x "$package" "$test_tools/extracted"; done
  )
  export CD_TEST_SSHD="$test_tools/extracted/usr/sbin/sshd"
  case "$(dpkg --print-architecture)" in
    amd64) multiarch=x86_64-linux-gnu ;;
    arm64) multiarch=aarch64-linux-gnu ;;
    *) printf 'Unsupported offline sshd test architecture\n' >&2; exit 1 ;;
  esac
  export CD_TEST_SSHD_LIBRARY_PATH="$test_tools/extracted/usr/lib/$multiarch:$test_tools/extracted/lib/$multiarch"
  export CD_TEST_NSS_WRAPPER="$test_tools/extracted/usr/lib/$multiarch/libnss_wrapper.so"
fi
# sshd -T also checks its compiled-in privilege-separation directory. Do not
# alter an existing one; remove only our own empty runtime directory on exit.
if [[ ! -e /run/sshd && ! -L /run/sshd ]]; then
  mkdir -m 0755 /run/sshd
  created_privsep_dir=1
fi
python3 -B "$script_dir/production-cd-ssh-access-test.py"
