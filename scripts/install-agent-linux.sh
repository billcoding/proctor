#!/usr/bin/env bash
set -euo pipefail

# Usage: sudo ./install-agent-linux.sh /path/to/proctor-agent http://teacher:8080 "学生名" "教室"
BIN_SRC=${1:-./bin/proctor-agent}
SERVER_URL=${2:-http://127.0.0.1:8080}
STUDENT=${3:-}
CLASSROOM=${4:-}
INSTALL_BIN=/usr/local/bin/proctor-agent
CONF_DIR=/etc/proctor
CONF_FILE=$CONF_DIR/agent.json

if [[ $EUID -ne 0 ]]; then
  echo "请使用 root 执行: sudo $0 ..." >&2
  exit 1
fi

install -d -m 755 "$CONF_DIR"
install -m 755 "$BIN_SRC" "$INSTALL_BIN"

cat >"$CONF_FILE" <<EOF
{
  "server_url": "${SERVER_URL}",
  "agent_id": "",
  "student_name": "${STUDENT}",
  "classroom": "${CLASSROOM}",
  "collect_interval_sec": 15,
  "top_n_processes": 30,
  "data_dir": "/var/lib/proctor",
  "log_file": "",
  "insecure_skip_verify": false
}
EOF
chmod 644 "$CONF_FILE"
install -d -m 755 /var/lib/proctor

"$INSTALL_BIN" install -config "$CONF_FILE"
"$INSTALL_BIN" start
"$INSTALL_BIN" status
echo "Linux systemd 服务已安装并启动"
