#!/usr/bin/env bash
# 打发布包 → dist/proctor-{server|agent}.tar.gz
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"
cd "$ROOT"

usage(){
  cat <<EOF
package / deploy

用法:
 ./deploy.sh package [server|agent|all] [选项]
 ./deploy.sh deploy  [server|agent|all] [选项]   # 同 package

说明:
 一体包含：二进制 + 默认配置 + systemd unit（Linux）
 含 agent 时，打包完成后默认发布 OTA 到 \$DATA_DIR/updates/<version>/（多版本共存）

env:
 DATA_DIR / UPDATES_DIR  OTA 目录（见 publish_update）
 SKIP_PUBLISH_UPDATE=1     跳过自动发布
 PUBLISH_UPDATE=0           同跳过

options:
 -S,--skip-build         跳过重建
 -U,--skip-update        跳过写入 updates/
 -L,--linux              编译 linux（默认打包目标为 linux/amd64）
 -D,--darwin / -W,--windows / -a,--all
 -v,--version <VER>      APP_VERSION（与 OTA version.json 一致）
 --force / --notes <T>   透传给 publish_update
EOF
}

COMP="all"
SKIP_BUILD=0
SKIP_PUBLISH=0
VER=""
BUILD_PASS=(-L)
PUBLISH_EXTRA=()

# 环境变量可关闭自动发布：SKIP_PUBLISH_UPDATE=1 或 PUBLISH_UPDATE=0
[[ "${SKIP_PUBLISH_UPDATE:-0}" == "1" ]] && SKIP_PUBLISH=1
[[ "${PUBLISH_UPDATE:-1}" == "0" ]] && SKIP_PUBLISH=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    -S|--skip-build) SKIP_BUILD=1; shift ;;
    -U|--skip-update) SKIP_PUBLISH=1; shift ;;
    -v|--version)
      [[ $# -ge 2 ]] || die "缺少版本号"
      VER="$2"; shift 2 ;;
    --force) PUBLISH_EXTRA+=(--force); shift ;;
    --notes)
      [[ $# -ge 2 ]] || die "缺少 --notes 文本"
      PUBLISH_EXTRA+=(--notes "$2"); shift 2 ;;
    --notes=*) PUBLISH_EXTRA+=(--notes "${1#--notes=}"); shift ;;
    -L|--linux|-D|--darwin|-W|--windows|-a|--all)
      BUILD_PASS=("$1"); shift ;;
    server|agent|all|s|a|srv|agt)
      COMP="$(normalize_component "$1")"; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ -n "$VER" ]] && export APP_VERSION="$VER"
# 与 build / publish_update 共用同一版本（避免子进程各自取时间戳）
APP_VERSION="${APP_VERSION:-$(date '+%Y-%m-%dT%H:%M:%S')}"
export APP_VERSION
OUT_DIR="${OUT_DIR:-$ROOT/dist}"
mkdir -p "$OUT_DIR"

comps=()
case "$COMP" in
  all) comps=(server agent) ;;
  *) comps=("$COMP") ;;
esac

if [[ "$SKIP_BUILD" != "1" ]]; then
  bash "$SCRIPT_DIR/build.sh" "$COMP" "${BUILD_PASS[@]}"
fi

pkg_one(){
  local comp="$1"
  local bin stage tarname cfg_src unit_src cfg_name
  bin="$(component_bin "$comp")"
  # 优先平台后缀 linux 包，否则裸名
  local bin_src=""
  for cand in \
    "$OUT_DIR/${bin}-linux-amd64" \
    "$OUT_DIR/${bin}-linux-arm64" \
    "$OUT_DIR/${bin}" \
    "$OUT_DIR/${bin}.exe"
  do
    if [[ -f "$cand" ]]; then bin_src="$cand"; break; fi
  done
  [[ -n "$bin_src" ]] || die "缺少二进制: $bin（请先 build）"

  stage="$(mktemp -d "${TMPDIR:-/tmp}/proctor-pkg.XXXXXX")"
  mkdir -p "$stage"

  cp "$bin_src" "$stage/$bin"
  chmod +x "$stage/$bin" || true

  if [[ "$comp" == "server" ]]; then
    cfg_src="$ROOT/configs/server.json"
    cfg_name="server.json"
    unit_src="$ROOT/configs/resources/proctor-server.service"
  else
    cfg_src="$ROOT/configs/agent.json"
    cfg_name="agent.json"
    unit_src="$ROOT/configs/resources/proctor-agent.service"
  fi
  cp "$cfg_src" "$stage/$cfg_name"
  cp "$unit_src" "$stage/$(component_unit "$comp")"
  # 打包 web 静态资源（仅 server）
  if [[ "$comp" == "server" && -d "$ROOT/web/static" ]]; then
    mkdir -p "$stage/web"
    cp -R "$ROOT/web/static" "$stage/web/"
  fi

  tarname="proctor-${comp}.tar.gz"
  tar -C "$stage" -czf "$OUT_DIR/$tarname" .
  rm -rf "$stage"
  log "package → $OUT_DIR/$tarname"
}

for c in "${comps[@]}"; do
  pkg_one "$c"
done
log "package done"

# 含 agent 时自动发布 OTA（仅写 updates/，不触碰其它 data）
want_publish=0
for c in "${comps[@]}"; do
  [[ "$c" == "agent" ]] && want_publish=1
done
if [[ "$want_publish" == "1" && "$SKIP_PUBLISH" != "1" ]]; then
  # 不透传 -S：打包可复用已有 linux 二进制，OTA 仍按矩阵交叉编译
  pub_args=(-v "$APP_VERSION")
  pub_args+=("${PUBLISH_EXTRA[@]+"${PUBLISH_EXTRA[@]}"}")
  log "package → publish_update (OTA → \$DATA_DIR/updates)"
  bash "$SCRIPT_DIR/publish_update.sh" "${pub_args[@]}"
elif [[ "$want_publish" == "1" ]]; then
  log "skip publish_update（-U / SKIP_PUBLISH_UPDATE / PUBLISH_UPDATE=0）"
fi
