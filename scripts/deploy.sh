#!/usr/bin/env bash
# 兼容入口：转调仓根 deploy.sh
set -euo pipefail
exec bash "$(cd "$(dirname "$0")/.." && pwd)/deploy.sh" "$@"
