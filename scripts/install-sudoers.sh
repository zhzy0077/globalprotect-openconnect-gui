#!/bin/sh
set -e
SUDOERS_FILE=/etc/sudoers.d/gpclient-gui
printf '%%sudo ALL=(ALL) NOPASSWD: /usr/bin/gpclient\n' > "$SUDOERS_FILE"
chmod 0440 "$SUDOERS_FILE"
echo "Written: $SUDOERS_FILE"
