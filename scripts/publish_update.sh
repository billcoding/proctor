#!/usr/bin/env bash
# 交叉编译 Agent 并发布到教师端 OTA 目录：<data_dir>/updates/<version>/
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"
cd "$ROOT"

usage(){
  cat <<EOF
publish_update / package_update

用法:
 ./deploy.sh publish_update [选项]
 ./deploy.sh package_update [选项]   # 同上

说明:
 交叉编译常用平台 Agent，写入 OTA 版本子目录并更新 index.json。
 多版本可共存：不会删除其它版本目录。
 版本号与构建 ldflags（APP_VERSION / -v）一致。

目录布局:
  \$UPDATES_DIR/index.json
  \$UPDATES_DIR/<version>/version.json
  \$UPDATES_DIR/<version>/proctor-agent-<os>-<arch>[.exe]

默认平台矩阵:
  linux/amd64  darwin/amd64  darwin/arm64  windows/amd64

env:
 DATA_DIR                 教师端数据根（默认 ./data）
 UPDATES_DIR              OTA 目录（默认 \$DATA_DIR/updates）
 APP_VERSION / OUT_DIR    与 build 相同
 FORCE_UPDATE=1           version.json force=true
 UPDATE_NOTES=...         version.json notes
 SKIP_PUBLISH_UPDATE=1     （由 package 传入时跳过；本命令忽略）

options:
 -S,--skip-build          跳过重建（要求 dist 已有对应二进制）
 -v,--version <VER>       APP_VERSION
 -n,--dry-run             只打印
 --force                  force=true
 --notes <TEXT>           更新说明
 --no-latest              写入版本但不把 latest 指到该版本
EOF
}

# 解析 DATA_DIR / UPDATES_DIR（相对路径相对仓库根）
resolve_abs_dir(){
  local d="$1"
  if [[ "$d" != /* ]]; then
    d="$ROOT/${d#./}"
  fi
  printf '%s' "$d"
}

normalize_version(){
  local v="$1"
  v="${v#v}"
  v="${v#V}"
  printf '%s' "$v"
}

SKIP_BUILD=0
VER=""
NOTES="${UPDATE_NOTES:-}"
FORCE="${FORCE_UPDATE:-0}"
DRY_RUN="${DRY_RUN:-0}"
SET_LATEST=1

while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    -S|--skip-build) SKIP_BUILD=1; shift ;;
    -v|--version)
      [[ $# -ge 2 ]] || die "缺少版本号"
      VER="$2"; shift 2 ;;
    -n|--dry-run) DRY_RUN=1; shift ;;
    --force) FORCE=1; shift ;;
    --notes)
      [[ $# -ge 2 ]] || die "缺少 --notes 文本"
      NOTES="$2"; shift 2 ;;
    --notes=*) NOTES="${1#--notes=}"; shift ;;
    --no-latest) SET_LATEST=0; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

[[ -n "$VER" ]] && export APP_VERSION="$VER"
OUT_DIR="${OUT_DIR:-$ROOT/dist}"
APP_VERSION="${APP_VERSION:-$(date '+%Y-%m-%dT%H:%M:%S')}"
APP_VERSION="$(normalize_version "$APP_VERSION")"
export APP_VERSION OUT_DIR CGO_ENABLED
export DRY_RUN

DATA_DIR="$(resolve_abs_dir "${DATA_DIR:-./data}")"
if [[ -n "${UPDATES_DIR:-}" ]]; then
  UPDATES_DIR="$(resolve_abs_dir "$UPDATES_DIR")"
else
  UPDATES_DIR="$DATA_DIR/updates"
fi

VERSION_DIR="$UPDATES_DIR/$APP_VERSION"

# goos:goarch
OTA_PLATFORMS=(
  "linux:amd64"
  "darwin:amd64"
  "darwin:arm64"
  "windows:amd64"
)

build_agent_plat(){
  local goos="$1" goarch="$2"
  local out name
  name="$(binary_name proctor-agent "$goos" "$goarch" 1)"
  out="$OUT_DIR/$name"
  if [[ -f "$out" && "$SKIP_BUILD" == "1" ]]; then
    log "reuse $out"
    printf '%s' "$out"
    return 0
  fi
  if [[ "$SKIP_BUILD" == "1" ]]; then
    die "缺少 $out（先 ./deploy.sh build agent，或去掉 -S）"
  fi
  mkdir -p "$OUT_DIR"
  log "build agent → $out (${goos}/${goarch})"
  if is_dry_run; then
    log "[dry-run] go build -o $out ./cmd/agent"
    printf '%s' "$out"
    return 0
  fi
  (
    export GOOS="$goos" GOARCH="$goarch" CGO_ENABLED="${CGO_ENABLED:-0}"
    go build -ldflags "$(go_ldflags)" -o "$out" ./cmd/agent
  )
  printf '%s' "$out"
}

log "publish_update version=${APP_VERSION}"
log "updates dir → $UPDATES_DIR"
log "version dir → $VERSION_DIR"

if ! is_dry_run; then
  mkdir -p "$VERSION_DIR" "$OUT_DIR"
fi

# 收集: key|filename|srcpath
ARTIFACT_LINES=()
for plat in "${OTA_PLATFORMS[@]}"; do
  goos="${plat%%:*}"
  goarch="${plat##*:}"
  src="$(build_agent_plat "$goos" "$goarch")"
  key="${goos}-${goarch}"
  dest_name="$(binary_name proctor-agent "$goos" "$goarch" 1)"
  dest="$VERSION_DIR/$dest_name"
  if is_dry_run; then
    log "[dry-run] cp $src → $dest"
  else
    [[ -f "$src" ]] || die "构建后仍缺少: $src"
    cp "$src" "$dest"
    chmod +x "$dest" 2>/dev/null || true
    log "publish → $dest"
  fi
  ARTIFACT_LINES+=("${key}|${dest_name}|${dest}")
done

write_manifest_and_index(){
  command -v python3 >/dev/null 2>&1 || die "需要 python3 以生成 version.json / index.json"
  local manifest="$VERSION_DIR/version.json"
  local index="$UPDATES_DIR/index.json"
  local notes="$NOTES"
  local created
  created="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  [[ -n "$notes" ]] || notes="published by deploy.sh publish_update"

  if is_dry_run; then
    log "[dry-run] write $manifest (version=${APP_VERSION})"
    log "[dry-run] update $index (latest=${SET_LATEST})"
    return 0
  fi

  python3 - "$manifest" "$index" "$APP_VERSION" "$FORCE" "$notes" "$created" "$SET_LATEST" "${ARTIFACT_LINES[@]}" <<'PY'
import hashlib, json, os, sys

manifest_path = sys.argv[1]
index_path = sys.argv[2]
version = sys.argv[3].lstrip("vV")
force_s = sys.argv[4]
notes = sys.argv[5]
created = sys.argv[6]
set_latest = sys.argv[7] in ("1", "true", "True", "yes")
force = force_s in ("1", "true", "True", "yes")

artifacts = {}
for item in sys.argv[8:]:
    key, file_name, full = item.split("|", 2)
    with open(full, "rb") as f:
        data = f.read()
    art = {
        "sha256": hashlib.sha256(data).hexdigest(),
        "size": len(data),
    }
    if file_name.endswith(".exe") or file_name != f"proctor-agent-{key}":
        art["file"] = file_name
    artifacts[key] = art

manifest = {
    "version": version,
    "force": force,
    "notes": notes,
    "created_at": created,
    "artifacts": artifacts,
}
os.makedirs(os.path.dirname(manifest_path) or ".", exist_ok=True)
with open(manifest_path, "w", encoding="utf-8") as f:
    json.dump(manifest, f, ensure_ascii=False, indent=2)
    f.write("\n")

idx = {"latest": "", "versions": []}
if os.path.isfile(index_path):
    try:
        with open(index_path, "r", encoding="utf-8") as f:
            idx = json.load(f)
    except Exception:
        idx = {"latest": "", "versions": []}
if not isinstance(idx, dict):
    idx = {"latest": "", "versions": []}
versions = idx.get("versions") or []
if not isinstance(versions, list):
    versions = []

entry = {
    "version": version,
    "notes": notes,
    "force": force,
    "created_at": created,
}
replaced = False
for i, e in enumerate(versions):
    if not isinstance(e, dict):
        continue
    ev = str(e.get("version", "")).lstrip("vV")
    if ev == version:
        # Keep original created_at if re-publishing same version.
        if e.get("created_at"):
            entry["created_at"] = e["created_at"]
            manifest["created_at"] = e["created_at"]
            with open(manifest_path, "w", encoding="utf-8") as f:
                json.dump(manifest, f, ensure_ascii=False, indent=2)
                f.write("\n")
        versions[i] = entry
        replaced = True
        break
if not replaced:
    versions.append(entry)

def ver_key(e):
    v = str(e.get("version", "")).lstrip("vV")
    parts = []
    core = v.split("-", 1)[0].split("+", 1)[0]
    for p in core.split("."):
        if p.isdigit():
            parts.append(int(p))
        else:
            return (0, v)
    while len(parts) < 3:
        parts.append(0)
    return (1, tuple(parts), v)

versions.sort(key=ver_key, reverse=True)
idx["versions"] = versions
if set_latest or not str(idx.get("latest") or "").strip():
    idx["latest"] = version
else:
    idx["latest"] = str(idx.get("latest", "")).lstrip("vV")

os.makedirs(os.path.dirname(index_path) or ".", exist_ok=True)
with open(index_path, "w", encoding="utf-8") as f:
    json.dump(idx, f, ensure_ascii=False, indent=2)
    f.write("\n")
print(manifest_path)
print(index_path)
PY
  log "manifest → $manifest"
  log "index → $index (latest updated=$SET_LATEST)"
}

write_manifest_and_index
log "publish_update done (version=${APP_VERSION})"
log "other version dirs under $UPDATES_DIR were left intact"
