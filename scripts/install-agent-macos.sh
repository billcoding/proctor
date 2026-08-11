#!/usr/bin/env bash
set -euo pipefail

# Usage: sudo ./install-agent-macos.sh /path/to/proctor-agent http://teacher:8911 "学生名" "教室"
BIN_SRC=${1:-./bin/proctor-agent}
SERVER_URL=${2:-http://127.0.0.1:8911}
STUDENT=${3:-}
CLASSROOM=${4:-}
INSTALL_BIN=/usr/local/bin/proctor-agent
CONF_DIR=/Library/Application\ Support/proctor
CONF_FILE=$CONF_DIR/agent.json

if [[ $EUID -ne 0 ]]; then
  echo "请使用 root 执行: sudo $0 ..." >&2
  exit 1
fi

mkdir -p "$CONF_DIR" /var/lib/proctor
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
  "log_file": "logs/agent.log",
  "insecure_skip_verify": false
}
EOF

"$INSTALL_BIN" install -config "$CONF_FILE"
"$INSTALL_BIN" start
"$INSTALL_BIN" status
echo "macOS launchd 服务已安装并启动"
echo "可用: launchctl print system/proctor-agent"
