#!/usr/bin/env bash
# 本机首次安装（Linux systemd；macOS/Windows 走 agent 内置服务命令）
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
# shellcheck source=_lib.sh
source "$SCRIPT_DIR/_lib.sh"
load_workspace_dotenv "$ROOT"
cd "$ROOT"

usage(){
  cat <<EOF
install

用法:
 ./deploy.sh install [server|agent|all] [选项]

说明:
 Linux：安装到 INSTALL_PREFIX（默认 /opt/proctor/{server|agent}）并注册 systemd
 macOS/Windows agent：复制二进制后调用 proctor-agent install

options:
 -I,--prefix <DIR>       安装前缀（默认 /opt/proctor）
 -N,--no-start           装完不启动
 -n,--dry-run            只打印
 -S,--skip-build         缺包时 package 跳过重建
 --server-url <URL>      写入 agent.json
 --student <NAME>        写入 agent.json
 --classroom <NAME>      写入 agent.json
 --listen <ADDR>         写入 server.json
 --admin-token <TOKEN>   写入 server.json
 --set key=value         写入对应 JSON（可多次）
EOF
}

COMP="all"
NO_START=0
SKIP_BUILD=0
CONFIG_SETS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    help|-h|--help) usage; exit 0 ;;
    -I|--prefix) INSTALL_PREFIX="$2"; shift 2 ;;
    -N|--no-start) NO_START=1; shift ;;
    -n|--dry-run) DRY_RUN=1; shift ;;
    -S|--skip-build) SKIP_BUILD=1; shift ;;
    --server-url) CONFIG_SETS+=("server_url=$2"); shift 2 ;;
    --student) CONFIG_SETS+=("student_name=$2"); shift 2 ;;
    --classroom) CONFIG_SETS+=("classroom=$2"); shift 2 ;;
    --listen) CONFIG_SETS+=("listen=$2"); shift 2 ;;
    --admin-token) CONFIG_SETS+=("admin_token=$2"); shift 2 ;;
    --set) CONFIG_SETS+=("$2"); shift 2 ;;
    server|agent|all|s|a|srv|agt) COMP="$(normalize_component "$1")"; shift ;;
    *) die "未知参数: $1" ;;
  esac
done

ensure_pkg(){
  local comp="$1"
  local tar="$ROOT/dist/proctor-${comp}.tar.gz"
  if [[ ! -f "$tar" ]]; then
    log "缺少 $tar，先 package"
    local args=("$comp")
    [[ "$SKIP_BUILD" == "1" ]] && args+=(-S)
    bash "$SCRIPT_DIR/package.sh" "${args[@]}"
  fi
  echo "$tar"
}

install_linux(){
  local comp="$1"
  local tar prefix bin unit cfg
  tar="$(ensure_pkg "$comp")"
  prefix="$(component_prefix "$comp")"
  bin="$(component_bin "$comp")"
  unit="$(component_unit "$comp")"
  need_root_or_writable "$prefix"

  local stage
  stage="$(mktemp -d "${TMPDIR:-/tmp}/proctor-inst.XXXXXX")"
  tar -C "$stage" -xzf "$tar"

  run_cmd mkdir -p "$prefix"
  run_cmd cp "$stage/$bin" "$prefix/$bin"
  run_cmd chmod +x "$prefix/$bin"
  if [[ "$comp" == "server" ]]; then
    cfg="server.json"
    [[ -f "$prefix/$cfg" ]] || run_cmd cp "$stage/$cfg" "$prefix/$cfg"
    if [[ -d "$stage/web" ]]; then
      run_cmd mkdir -p "$prefix/web"
      run_cmd cp -R "$stage/web/." "$prefix/web/"
    fi
    # 修正 unit 中的路径
    local unit_body
    unit_body="$(sed "s|/opt/proctor/server|${prefix}|g" "$stage/$unit")"
    if has_systemd; then
      if is_dry_run; then
        log "[dry-run] write $SYSTEMD_DIR/$unit"
      else
        printf '%s\n' "$unit_body" >"$SYSTEMD_DIR/$unit"
        systemctl daemon-reload
        systemctl enable "$unit"
        [[ "$NO_START" == "1" ]] || systemctl restart "$unit"
      fi
    else
      log "WARN: 无 systemd，仅安装文件到 $prefix"
    fi
  else
    cfg="agent.json"
    [[ -f "$prefix/$cfg" ]] || run_cmd cp "$stage/$cfg" "$prefix/$cfg"
    local unit_body
    unit_body="$(sed "s|/opt/proctor/agent|${prefix}|g" "$stage/$unit")"
    if has_systemd; then
      if is_dry_run; then
        log "[dry-run] write $SYSTEMD_DIR/$unit"
      else
        printf '%s\n' "$unit_body" >"$SYSTEMD_DIR/$unit"
        systemctl daemon-reload
        systemctl enable "$unit"
        [[ "$NO_START" == "1" ]] || systemctl restart "$unit"
      fi
    else
      # 回退到内置服务注册
      if [[ "$NO_START" == "1" ]]; then
        run_cmd "$prefix/$bin" install -config "$prefix/$cfg"
      else
        run_cmd "$prefix/$bin" install -config "$prefix/$cfg"
        run_cmd "$prefix/$bin" start
      fi
    fi
  fi

  if [[ ${#CONFIG_SETS[@]} -gt 0 ]]; then
    run_cmd json_set_file "$prefix/$cfg" "${CONFIG_SETS[@]}"
    if has_systemd && [[ "$NO_START" != "1" ]]; then
      run_cmd systemctl restart "$unit" || true
    fi
  fi
  rm -rf "$stage"
  log "installed $comp → $prefix"
}

install_agent_service_native(){
  # macOS / Windows：用 agent 内置 kardianos/service
  local comp=agent
  local tar prefix bin cfg
  tar="$(ensure_pkg "$comp")"
  prefix="$(component_prefix "$comp")"
  bin="$(component_bin "$comp")"
  cfg="agent.json"
  need_root_or_writable "$prefix"
  local stage
  stage="$(mktemp -d "${TMPDIR:-/tmp}/proctor-inst.XXXXXX")"
  tar -C "$stage" -xzf "$tar"
  run_cmd mkdir -p "$prefix"
  run_cmd cp "$stage/$bin" "$prefix/$bin"
  run_cmd chmod +x "$prefix/$bin" || true
  [[ -f "$prefix/$cfg" ]] || run_cmd cp "$stage/$cfg" "$prefix/$cfg"
  if [[ ${#CONFIG_SETS[@]} -gt 0 ]]; then
    run_cmd json_set_file "$prefix/$cfg" "${CONFIG_SETS[@]}"
  fi
  run_cmd "$prefix/$bin" install -config "$prefix/$cfg"
  [[ "$NO_START" == "1" ]] || run_cmd "$prefix/$bin" start
  rm -rf "$stage"
  log "installed agent service → $prefix"
}

comps=()
case "$COMP" in
  all) comps=(server agent) ;;
  *) comps=("$COMP") ;;
esac

for c in "${comps[@]}"; do
  case "$(os_family)" in
    linux) install_linux "$c" ;;
    darwin|windows)
      [[ "$c" == "agent" ]] || die "本机 install 在 $(os_family) 上目前仅支持 agent（server 请用 run 或 Linux）"
      install_agent_service_native
      ;;
    *) die "不支持的系统: $(uname -s)" ;;
  esac
done
log "install done"
