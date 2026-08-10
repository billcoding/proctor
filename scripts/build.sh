#!/usr/bin/env bash
# 编译 server / agent → dist/
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"
cd "$ROOT"

usage(){
  cat <<EOF
build

用法:
 ./deploy.sh build [server|agent|all] [平台 flag...]

说明:
 无平台 flag 时默认编译当前主机 OS/ARCH → dist/<bin>
 指定 -D/-L/-W/-a 时 → dist/<bin>-<goos>-<goarch>

env:
 GOOS / GOARCH / APP_VERSION / CGO_ENABLED / OUT_DIR

options:
 -D,--darwin     编译 darwin
 -L,--linux      编译 linux
 -W,--windows    编译 windows
 -a,--all        一次编译三端
EOF
}

parse_build_platforms "$@"
if [[ "${BUILD_WANT_HELP:-0}" -eq 1 ]]; then
  usage
  exit 0
fi

COMP="all"
REMAIN=()
for a in "${BUILD_REMAINING[@]+"${BUILD_REMAINING[@]}"}"; do
  if c="$(normalize_component "$a" 2>/dev/null)"; then
    COMP="$c"
  else
    case "$a" in
      amd64|arm64|arm|386) ;;
      *) die "多余/未知参数: $a（见 ./deploy.sh build -h）" ;;
    esac
  fi
done

OUT_DIR="${OUT_DIR:-$ROOT/dist}"
APP_VERSION="${APP_VERSION:-$(date '+%Y-%m-%dT%H:%M:%S')}"
export APP_VERSION OUT_DIR CGO_ENABLED
mkdir -p "$OUT_DIR"

build_one(){
  local comp="$1" goos="$2" goarch="$3" with_platform="$4"
  local bin pkg out
  bin="$(component_bin "$comp")"
  case "$comp" in
    server) pkg="./cmd/server" ;;
    agent) pkg="./cmd/agent" ;;
  esac
  out="$OUT_DIR/$(binary_name "$bin" "$goos" "$goarch" "$with_platform")"
  log "build ${comp} → ${out} (${goos}/${goarch})"
  (
    export GOOS="$goos" GOARCH="$goarch" CGO_ENABLED="${CGO_ENABLED:-0}"
    go build -ldflags "$(go_ldflags)" -o "$out" "$pkg"
  )
}

comps=()
case "$COMP" in
  all) comps=(server agent) ;;
  server|agent) comps=("$COMP") ;;
esac

if [[ ${#BUILD_PLATFORMS[@]} -eq 0 ]]; then
  goos="${GOOS:-$(os_family)}"
  [[ "$goos" == "other" ]] && goos=linux
  goarch="$(default_goarch_for "$goos")"
  for c in "${comps[@]}"; do
    build_one "$c" "$goos" "$goarch" 0
  done
else
  for plat in "${BUILD_PLATFORMS[@]}"; do
    goos="${plat%%:*}"
    goarch="${plat##*:}"
    for c in "${comps[@]}"; do
      build_one "$c" "$goos" "$goarch" 1
    done
  done
fi

log "build done"
