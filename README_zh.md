# Proctor

English: [README.md](./README.md)

基于 Go 的学生电脑日常监管系统：学生机 Agent 采集并执行策略，教师端 Server 提供 Web 管控台。

## 功能

- **进程管理**：进程列表、违规进程识别、远程结束、黑名单/白名单自动查杀
- **网络管理**：连接列表、受限域名告警（反查主机名匹配）
- **系统资源**：CPU / 内存 / Load / 运行时长
- **磁盘监管**：挂载点容量与使用率告警
- **策略下发**：多策略、按机分配、黑/白名单、阈值、采集间隔等，心跳时同步
- **远程指令**：ping、学生端弹窗消息、杀进程、关机/重启
- **远程文件**：浏览/上传/下载/新建/重命名/删除学生机文件（单文件上限 4MB）
- **远程终端**：Agent 反向 Shell，或教师端 SSH 直连学生机（需 OpenSSH）
- **系统服务**：Agent 支持注册为
  - macOS：`launchd`
  - Linux：`systemd`
  - Windows：Windows Service
- **Agent 自升级（OTA）**：教师端放置新二进制后，学生机可自动或手动升级

## 架构

```
学生机 proctor-agent  --heartbeat/commands-->  教师机 proctor-server + Web 控制台
```

## 快速开始（推荐：deploy.sh）

统一运维入口（一键部署）：

```bash
./deploy.sh help
./deploy.sh build all -a          # 交叉编译 mac/linux/windows
./deploy.sh run server            # 前台跑教师端
./deploy.sh run agent             # 前台跑学生机
./deploy.sh deploy all            # 打 linux 发布包 → dist/*.tar.gz；含 agent 时自动写 OTA → ./data/updates/
./deploy.sh publish_update -v 0.2.0   # 仅交叉编译 agent 并发布到 ./data/updates/
sudo ./deploy.sh install server
sudo ./deploy.sh install agent --server-url http://教师机IP:8080 --student "张三" --classroom "高一1班"
./deploy.sh status
# Linux 学生机（SSH + systemd）
./deploy.sh remote_install agent -H root@学生机IP --server-url http://教师机IP:8080
# Windows 学生机（需已启用 OpenSSH Server；管理员账号；自动探测或 --os windows）
./deploy.sh remote_install agent -H Administrator@学生机IP --os windows \
  --server-url http://教师机IP:8080 --student "张三" --classroom "高一1班"
```

可选：复制 `configs/.env.example` → `.env`（`REMOTE_SSH_*` / `REMOTE_OS` / `SERVER_URL` 等会自动加载）。

**远程部署说明**：`remote_*` 一律走 SSH（`REMOTE_SSH_*`），不支持用 RDP 做无人值守安装。Linux 假定 systemd；Windows 仅支持 **agent**，远端执行 `install-agent-windows.ps1` 注册 Windows Service。RDP 只适合人工登录排查或首次打开 OpenSSH。

### 兼容：Makefile / 直接二进制

```bash
make deps && make build
./bin/proctor-server -config ./configs/server.json
./bin/proctor-agent run -server http://教师机IP:8080 -student "张三" -classroom "高一1班"
```

浏览器打开 `http://127.0.0.1:8080`，会弹出 HTTP Basic Auth（默认 `proctor` / `proctor`）；进入后控制台还需管理令牌：`proctor-admin`。

### 系统服务

- **推荐**：`sudo ./deploy.sh install agent|server`（Linux systemd；macOS/Windows 的 agent 走内置服务注册）
- **兼容旧脚本**：`scripts/install-agent-{linux,macos}.sh` / `install-agent-windows.ps1`
- **手动**：`proctor-agent install|start|stop|status|uninstall`

## 配置

### Server (`configs/server.json`)

| 字段 | 说明 |
|------|------|
| `listen` | 监听地址，默认 `:8080` |
| `admin_token` | Web/API 管理令牌 |
| `basic_auth_user` / `basic_auth_password` | 控制台与管理 API 的 HTTP Basic Auth（默认 `proctor` / `proctor`）；密码为空则关闭 |
| `agent_token` | 可选；设置后 Agent 须带 `X-Agent-Token` |
| `db_path` | SQLite 路径 |
| `online_after_sec` | 超过该秒数未心跳视为离线 |

### Agent (`configs/agent.json`)

| 字段 | 说明 |
|------|------|
| `server_url` | 教师端地址 |
| `agent_token` | 与服务端 `agent_token` 一致（若服务端启用） |
| `student_name` / `classroom` | 展示信息（控制台可改，改后心跳不再覆盖） |
| `collect_interval_sec` | 本地采集间隔（可被策略覆盖） |
| `data_dir` | Agent ID 等本地数据目录 |
| `log_file` | 可选日志文件路径 |
| `insecure_skip_verify` | HTTPS 自签证书时跳过校验 |
| `auto_update` | 是否启用心跳后自动 OTA（默认 `true`，便于机房；设为 `false` 可关闭） |

### Agent 自升级（OTA，多版本）

推荐用脚本一键发布（写入教师端 `data_dir/updates/<version>/`，默认 `./data/updates`）：

```bash
# 打包时自动发布（含 agent 组件时）
./deploy.sh package all -v 0.2.0
# 或仅发布 OTA（linux/amd64、darwin/amd64、darwin/arm64、windows/amd64）
./deploy.sh publish_update -v 0.2.0
./deploy.sh publish_update -v 0.1.0 --no-latest   # 共存旧版本但不改 latest
# 覆盖目录：DATA_DIR=./data 或 UPDATES_DIR=./data/updates
# 打包但不写 updates：./deploy.sh package all -U
```

1. 目录布局（按版本分子目录，多版本可共存；新发布不会删其它版本）：

```text
data/updates/
  index.json                      # latest + versions[]
  0.2.0/
    version.json
    proctor-agent-linux-amd64
    proctor-agent-darwin-amd64
    proctor-agent-darwin-arm64
    proctor-agent-windows-amd64.exe
  0.1.0/
    version.json
    ...
```

兼容：若仍存在旧的扁平 `data/updates/version.json` + 根目录二进制，服务端可读作单一版本；**新发布只写入 `updates/<version>/`**。

2. `index.json` / 各版本 `version.json` 由 `publish_update` 自动生成（含 sha256/size）；示例见 `configs/updates/`。

`size` 为 0 或省略时，服务端用实际文件大小；`sha256` 可选但建议填写。二进制默认文件名为 `proctor-agent-{os}-{arch}[.exe]`。

3. Web 控制台：侧栏「版本管理」可查看共存版本、标记 latest、删除非最新版本；学生机详情或版本页可「升级到所选版本」。列表中「有更新」可一键升到 latest。

4. Agent 行为：
   - `auto_update: true` 时，心跳成功后最多每 5 分钟检查一次；只跟随 **latest**（更高版本则下载 → 校验 → 替换 → 重启）
   - 教师指定升级：下发 `update` 指令（payload.version），可升到任意已发布版本（含降级）
   - 手动：`proctor-agent update` 或 `proctor-agent update 0.2.0`
   - 鉴权与心跳相同：可选 `X-Agent-Token`；路径在 `/api/agent/*` 下，不受网页 Basic Auth 拦截

编译发布时请用 `-ldflags "-X github.com/billcoding/proctor/internal/agent.Version=x.y.z"`（`make` / `deploy.sh publish_update -v` / `package -v` 已支持），以便版本比较生效。

## API 摘要

- `POST /api/agent/heartbeat` — Agent 上报（可选 `X-Agent-Token`；不受网页 Basic Auth 拦截）
- `GET /api/agent/update/check?os=&arch=&version=&target=` — 查询更新（`target` 可选，默认 latest）
- `GET /api/agent/update/download/{version}/{os}/{arch}` — 按版本下载；旧路径 `/download/{os}/{arch}` 仍指向 latest
- `GET /api/updates` — 版本列表；`GET /api/updates/{version}` 详情
- `PUT /api/updates/latest` — 标记默认 latest（`{"version":"0.2.0"}`）
- `DELETE /api/updates/{version}` — 删除非 latest 版本目录
- `POST /api/agents/{id}/update` — 单机升级（`{"version":"0.2.0"}`）
- `POST /api/agents/update` — 批量升级（`agent_ids` 或 `classroom` + `version`）
- `GET /api/agents` — 学生机列表（需 Basic Auth + `X-Admin-Token`）
- `GET/PATCH /api/agents/{id}` — 详情 / 改学生名与教室
- `POST /api/agents/{id}/command` — 下发指令（`ping` / `message` / `kill_process` / `shutdown` / `restart` / `update`）
- `POST /api/agents/{id}/fs` — 远程文件任务（`roots` / `list` / `read` / `write` / `mkdir` / `delete` / `rename`）
- `GET /api/fs/jobs/{id}` — 查询文件任务结果
- `POST /api/agents/{id}/policy` — 按机分配策略
- `GET/POST /api/policies`、`GET/PUT/DELETE /api/policies/{id}` — 策略读写
- `GET /api/alerts` — 告警列表

## 目录结构

```
deploy.sh          统一运维入口
cmd/agent          学生机入口（含服务安装）
cmd/server         教师端入口
internal/agent     采集、策略执行、服务封装
internal/server    API 与 SQLite 存储
internal/model     共享数据结构
web/static         管控台前端
configs            示例配置
scripts/           deploy 子脚本、unit 模板、旧安装脚本
```

## 说明

- 查杀进程、安装系统服务、远程关机需要相应系统权限。
- 域名黑名单会结合反查主机名与 IP:port 做匹配，适合课堂轻量提醒，不是完整网关防火墙。
- Linux 消息弹窗依赖 `zenity` 或 `notify-send`；macOS 用系统对话框；Windows 用 MessageBox。
- 默认策略含常见娱乐软件/站点示例（含 WeGame：进程 `wegame`，域名 `wegame.qq.com` / `wegame.com`），可按学校实际修改。升级或重启服务端后，若默认策略尚无上述项会自动补入；自定义策略需在策略页手动添加。
