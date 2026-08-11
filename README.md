# Proctor

中文文档见 [README_zh.md](./README_zh.md)

A Go-based classroom endpoint monitoring system: student Agents collect metrics and enforce policies; the teacher Server provides a Web console.

## Features

- **Process management**: process list, policy violations, remote kill, blacklist/whitelist auto-kill
- **Network management**: connection list, restricted-domain alerts (reverse-DNS matching)
- **System resources**: CPU / memory / load / uptime
- **Disk monitoring**: mount capacity and usage alerts
- **Policy sync**: multi-policy, per-agent assignment, black/whitelist, thresholds, collect interval — synced on heartbeat
- **Remote commands**: ping, student pop-up message, kill process, shutdown/restart
- **Remote files**: browse/upload/download/mkdir/rename/delete on student PCs (4MB per file)
- **Remote terminal**: Agent reverse shell, or direct SSH from teacher server (OpenSSH required)
- **System service**: Agent can register as
  - macOS: `launchd`
  - Linux: `systemd`
  - Windows: Windows Service
- **Agent self-update (OTA)**: publish a new binary on the teacher server; agents upgrade automatically or on demand

## Architecture

```
Student PC proctor-agent  --heartbeat/commands-->  Teacher PC proctor-server + Web console
```

## Quick start (recommended: deploy.sh)

Unified ops entry:

```bash
./deploy.sh help
./deploy.sh build all -a          # cross-compile mac/linux/windows
./deploy.sh run server            # run teacher server in foreground
./deploy.sh run agent             # run student agent in foreground
./deploy.sh deploy all            # linux packages → dist/*.tar.gz; with agent, also publish OTA → ./data/updates/
./deploy.sh publish_update -v 0.2.0   # cross-build agent and publish to ./data/updates/ only
sudo ./deploy.sh install server
sudo ./deploy.sh install agent --server-url http://TEACHER_IP:8911 --student "Alice" --classroom "Class-1"
./deploy.sh status
# Linux student host (SSH + systemd)
./deploy.sh remote_install agent -H root@STUDENT_IP --server-url http://TEACHER_IP:8911
# Windows student host (OpenSSH Server required; admin account; auto-detect or --os windows)
./deploy.sh remote_install agent -H Administrator@STUDENT_IP --os windows \
  --server-url http://TEACHER_IP:8911 --student "Alice" --classroom "Class-1"
```

Optional: copy `configs/.env.example` → `.env` (`REMOTE_SSH_*` / `REMOTE_OS` / `SERVER_URL`, etc. are loaded automatically).

**Remote deploy notes**: all `remote_*` commands use SSH (`REMOTE_SSH_*`); RDP is not supported for unattended install. Linux assumes systemd; Windows supports **agent** only and runs `install-agent-windows.ps1` remotely to register a Windows Service. Use RDP only for manual troubleshooting or to enable OpenSSH the first time.

### Compatibility: Makefile / raw binaries

```bash
make deps && make build
./bin/proctor-server -config ./configs/server.json
./bin/proctor-agent run -server http://TEACHER_IP:8911 -student "Alice" -classroom "Class-1"
```

Open `http://127.0.0.1:8911` in a browser. HTTP Basic Auth prompts first (default `proctor` / `proctor`); then use admin token `proctor-admin` in the console.

### System service

- **Recommended**: `sudo ./deploy.sh install agent|server` (Linux systemd; macOS/Windows agent uses built-in service registration)
- **Legacy scripts**: `scripts/install-agent-{linux,macos}.sh` / `install-agent-windows.ps1`
- **Manual**: `proctor-agent install|start|stop|status|uninstall`

Agent logs default to `logs/agent.log` under the process working directory (packaged systemd: `WorkingDirectory=/opt/proctor/agent`; built-in `install`: `data_dir`). If a service has no working directory, logs are relative to the process cwd.

## Configuration

### Server (`configs/server.json`)

| Field | Description |
|------|------|
| `listen` | Listen address, default `:8911` |
| `admin_token` | Web/API admin token |
| `basic_auth_user` / `basic_auth_password` | HTTP Basic Auth for console & management APIs (default `proctor` / `proctor`); empty password disables |
| `agent_token` | Optional; when set, agents must send `X-Agent-Token` |
| `db_path` | SQLite path |
| `online_after_sec` | Offline if no heartbeat for this many seconds |

### Agent (`configs/agent.json`)

| Field | Description |
|------|------|
| `server_url` | Teacher server URL |
| `agent_token` | Must match server `agent_token` when enabled |
| `student_name` / `classroom` | Display info (console edits are not overwritten by heartbeat) |
| `collect_interval_sec` | Local collect interval (may be overridden by policy) |
| `data_dir` | Local data directory (agent ID, etc.) |
| `log_file` | Log path (default `logs/agent.log` under cwd; empty uses the same, with fallback to `<exeDir>/logs/` if cwd is unusable). Current day is always `agent.log`; prior days archive as `agent.YYYY-MM-DD.log` |
| `insecure_skip_verify` | Skip TLS verify for self-signed HTTPS |
| `auto_update` | Enable OTA after heartbeat (default `true` for lab fleets; set `false` to disable) |

### Agent self-update (OTA, multi-version)

Publish with the deploy scripts (writes to `data_dir/updates/<version>/`, default `./data/updates`):

```bash
# package auto-publishes when the agent component is included
./deploy.sh package all -v 0.2.0
# or publish OTA only (linux/amd64, darwin/amd64, darwin/arm64, windows/amd64)
./deploy.sh publish_update -v 0.2.0
./deploy.sh publish_update -v 0.1.0 --no-latest   # keep coexistence without changing latest
# override path: DATA_DIR=./data or UPDATES_DIR=./data/updates
# package without writing updates: ./deploy.sh package all -U
```

1. Layout (one folder per version; new publishes do not remove other versions):

```text
data/updates/
  index.json
  0.2.0/
    version.json
    proctor-agent-linux-amd64
    ...
  0.1.0/
    ...
```

Legacy flat `updates/version.json` + root binaries are still readable; new publishes only write version subdirs.

2. Web console: **Versions** menu lists coexisting builds, can mark latest / delete non-latest, and push a chosen version to a student machine.

3. Agent behavior:
   - `auto_update` follows **latest** only
   - Teacher-directed upgrade via `POST /api/agents/{id}/update` (`update` command with `version`)
   - Manual: `proctor-agent update` or `proctor-agent update 0.2.0`

Build with `-ldflags "-X github.com/billcoding/proctor/internal/agent.Version=x.y.z"` (`make` / `deploy.sh publish_update -v` / `package -v`) so version comparison works.

## API summary

- `POST /api/agent/heartbeat` — agent report (optional `X-Agent-Token`; not gated by web Basic Auth)
- `GET /api/agent/update/check?os=&arch=&version=&target=` — check update (`target` optional, default latest)
- `GET /api/agent/update/download/{version}/{os}/{arch}` — download by version (legacy `/download/{os}/{arch}` = latest)
- `GET /api/updates` / `GET /api/updates/{version}` — list / detail
- `PUT /api/updates/latest` / `DELETE /api/updates/{version}` — mark latest / delete non-latest
- `POST /api/agents/{id}/update` / `POST /api/agents/update` — single / batch upgrade
- `GET /api/agents` — student list (requires Basic Auth + `X-Admin-Token`)
- `GET/PATCH /api/agents/{id}` — detail / update student name & classroom
- `POST /api/agents/{id}/command` — issue a command (`ping` / `message` / `kill_process` / `shutdown` / `restart` / `update`)
- `POST /api/agents/{id}/fs` — remote filesystem job (`roots` / `list` / `read` / `write` / `mkdir` / `delete` / `rename`)
- `GET /api/fs/jobs/{id}` — poll filesystem job result
- `POST /api/agents/{id}/policy` — assign policy to agent
- `GET/POST /api/policies`, `GET/PUT/DELETE /api/policies/{id}` — policy CRUD
- `GET /api/alerts` — alert list

## Layout

```
deploy.sh          unified ops entry
cmd/agent          student agent entry (incl. service install)
cmd/server         teacher server entry
internal/agent     collect, policy enforce, service wrapper
internal/server    API and SQLite storage
internal/model     shared data structures
web/static         console frontend
configs            sample configs
scripts/           deploy helpers, unit templates, legacy installers
```

## Notes

- Killing processes, installing system services, and remote shutdown require appropriate privileges.
- Domain blacklists match reverse-DNS hostnames and IP:port — lightweight classroom alerts, not a full gateway firewall.
- Message pop-ups: Linux needs `zenity` or `notify-send`; macOS uses a system dialog; Windows uses MessageBox.
- Default policy includes sample entertainment apps/sites (including WeGame: process `wegame`, domains `wegame.qq.com` / `wegame.com`); adjust for your school. On server upgrade/restart, missing WeGame entries are merged into the default policy; custom policies must be updated on the policy page.
