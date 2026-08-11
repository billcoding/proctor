const VALID_VIEWS = ["dashboard", "agents", "monitor", "files", "terminal", "alerts", "commands", "updates", "policies"];
const VALID_MONITOR_TABS = ["overview", "speed", "nics", "disk", "procs", "nets"];

function storedChoice(key, allowed, fallback) {
  const v = localStorage.getItem(key);
  return allowed.includes(v) ? v : fallback;
}

const state = {
  token: localStorage.getItem("proctor_token") || "proctor-admin",
  view: storedChoice("proctor_view", VALID_VIEWS, "dashboard"),
  agents: [],
  policies: [],
  selectedAgentId: localStorage.getItem("proctor_selected_agent") || null,
  selectedPolicyId: "default",
  policy: null,
  monitorTab: storedChoice("proctor_monitor_tab", VALID_MONITOR_TABS, "overview"),
  monitorProcFilter: localStorage.getItem("proctor_monitor_proc_filter") || "",
  monitorNetFilter: localStorage.getItem("proctor_monitor_net_filter") || "",
  // Safe default: missing key ⇒ read-only. Persist "1"/"0" (also accept true/false).
  monitorProcReadOnly: (() => {
    const v = localStorage.getItem("proctor_monitor_proc_readonly");
    if (v === null) return true;
    return v !== "0" && v !== "false";
  })(),
  monitorProcs: [],
  monitorNets: [],
  alertFilter: localStorage.getItem("proctor_alert_filter") || "",
  alerts: [],
  commands: [],
  commandFilterAgent: localStorage.getItem("proctor_command_filter_agent") || "",
  commandFilterType: localStorage.getItem("proctor_command_filter_type") || "",
  commandFilterStatus: localStorage.getItem("proctor_command_filter_status") || "",
  commandFilterKeyword: localStorage.getItem("proctor_command_filter_keyword") || "",
  refreshSec: Number(localStorage.getItem("proctor_refresh_sec") || 5),
  refreshTimer: null,
  refreshing: false,
  fsPath: "",
  fsAgentId: null,
  fsBusy: false,
  fsEntries: [],
  // Safe default: missing key ⇒ read-only. Persist "1"/"0" (also accept true/false).
  fsReadOnly: (() => {
    const v = localStorage.getItem("proctor_fs_readonly");
    if (v === null) return true;
    return v !== "0" && v !== "false";
  })(),
  term: null,
  termFit: null,
  termWS: null,
  termAgent: null,
  latestAgentVersion: "",
  updateVersions: [],
};

function setSelectedAgentId(id) {
  state.selectedAgentId = id || null;
  if (state.selectedAgentId) localStorage.setItem("proctor_selected_agent", state.selectedAgentId);
  else localStorage.removeItem("proctor_selected_agent");
}

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => [...document.querySelectorAll(sel)];

function headers(json = true) {
  const h = { "X-Admin-Token": state.token };
  if (json) h["Content-Type"] = "application/json";
  return h;
}

async function api(path, opts = {}) {
  const res = await fetch(path, {
    ...opts,
    headers: { ...headers(!(opts.body instanceof FormData) && opts.method && opts.method !== "GET"), ...(opts.headers || {}) },
  });
  const data = await res.json();
  if (!res.ok || data.ok === false) throw new Error(data.error || res.statusText);
  return data;
}

function fmtBytes(n) {
  if (!n && n !== 0) return "-";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  let v = Number(n);
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}

function fmtSpeed(bps) {
  const v = Number(bps) || 0;
  if (v < 1024) return `${v.toFixed(0)} B/s`;
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB/s`;
  if (v < 1024 * 1024 * 1024) return `${(v / 1024 / 1024).toFixed(2)} MB/s`;
  return `${(v / 1024 / 1024 / 1024).toFixed(2)} GB/s`;
}

function fmtPps(v) {
  const n = Number(v) || 0;
  if (n < 1000) return `${n.toFixed(0)} pps`;
  return `${(n / 1000).toFixed(1)}k pps`;
}

function fmtNum(n) {
  const v = Number(n) || 0;
  return v.toLocaleString();
}

function fmtTime(s) {
  if (!s) return "-";
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return s;
  return d.toLocaleString();
}

function meterClass(p) {
  if (p >= 90) return "danger";
  if (p >= 75) return "warn";
  return "";
}

function setView(name) {
  const view = VALID_VIEWS.includes(name) ? name : "dashboard";
  state.view = view;
  localStorage.setItem("proctor_view", view);
  $$(".nav-item").forEach((b) => b.classList.toggle("active", b.dataset.view === view));
  $$(".view").forEach((v) => v.classList.add("hidden"));
  $(`#view-${view}`).classList.remove("hidden");
  const titles = {
    dashboard: "总览",
    agents: "学生机",
    monitor: "监控",
    files: "文件",
    terminal: "终端",
    policies: "策略",
    alerts: "告警",
    commands: "指令",
    updates: "版本管理",
  };
  $("#viewTitle").textContent = titles[view] || view;
  syncAutoRefreshCtl();
  refresh();
}

function syncAutoRefreshCtl() {
  const ctl = $(".refresh-ctl");
  if (ctl) ctl.classList.toggle("hidden", state.view !== "monitor");
}

function updateClock() {
  $("#clock").textContent = new Date().toLocaleString();
}

async function refresh(opts = {}) {
  const silent = !!opts.silent;
  if (state.refreshing) return;
  state.refreshing = true;
  try {
    if (state.view === "dashboard") await loadDashboard();
    if (state.view === "agents") await loadAgents();
    if (state.view === "monitor") await loadMonitor();
    if (state.view === "files") await loadFiles();
    if (state.view === "terminal") await loadTerminal();
    if (state.view === "alerts") await loadAlerts();
    if (state.view === "policies") await loadPolicies();
    if (state.view === "commands") await loadCommands();
    if (state.view === "updates") await loadUpdates();
  } catch (e) {
    console.error(e);
    if (!silent) alert("加载失败: " + e.message);
  } finally {
    state.refreshing = false;
  }
}

function setupAutoRefresh() {
  if (state.refreshTimer) {
    clearInterval(state.refreshTimer);
    state.refreshTimer = null;
  }
  const sec = Number(state.refreshSec);
  if (!sec || sec <= 0) return;
  state.refreshTimer = setInterval(() => {
    if (document.visibilityState !== "visible") return;
    // Auto-refresh is monitor-only; other views load on enter or via manual refresh.
    if (state.view !== "monitor") return;
    refresh({ silent: true });
  }, sec * 1000);
}

async function loadDashboard() {
  const [{ stats }, { agents }, latest] = await Promise.all([
    api("/api/stats"),
    api("/api/agents"),
    fetchLatestAgentVersion(),
  ]);
  state.agents = agents || [];
  state.latestAgentVersion = latest;
  $("#statGrid").innerHTML = [
    ["在线设备", stats.online_agents, `共 ${stats.total_agents} 台`],
    ["离线设备", stats.offline_agents, "超过心跳阈值"],
    ["未确认告警", stats.alert_count, "需关注"],
    ["平均负载", `${(stats.avg_cpu || 0).toFixed(1)}%`, `内存 ${(stats.avg_mem || 0).toFixed(1)}%`],
  ].map(([label, value, hint]) => `
    <div class="stat"><div class="label">${label}</div><div class="value">${value}</div><div class="hint">${hint}</div></div>
  `).join("");

  const list = state.agents;
  $("#dashAgents").innerHTML = table(
    ["状态", "学生", "主机", "教室", "系统", "版本", "IP", "最后心跳"],
    list.map((a) => [
      badgeOnline(a.online),
      esc(a.student_name || "-"),
      esc(a.hostname),
      esc(a.classroom || "-"),
      `${esc(a.os)}/${esc(a.arch)}`,
      formatAgentVersionCell(a),
      esc(a.ip),
      fmtTime(a.last_seen),
    ]),
    list.map((a) => a.id)
  );
  bindAgentRows("#dashAgents");
  bindUpgradeLatestButtons("#dashAgents");
}

async function loadAgents() {
  const [{ agents }, { policies }, latest] = await Promise.all([
    api("/api/agents"),
    api("/api/policies"),
    fetchLatestAgentVersion(),
  ]);
  state.agents = agents || [];
  state.policies = policies || [];
  state.latestAgentVersion = latest;
  renderAgentSelect("#agentsAgentSelect");
  renderAgentsList();
  if (state.selectedAgentId && state.agents.some((a) => a.id === state.selectedAgentId)) {
    await showAgent(state.selectedAgentId);
  } else {
    $("#agentDetail").innerHTML = '<div class="empty">选择一台学生机查看详情</div>';
  }
}

function renderAgentsList() {
  const el = $("#agentsList");
  if (!el) return;
  const list = state.agents;
  const selected = state.selectedAgentId || "";
  el.innerHTML = table(
    ["状态", "学生", "主机", "教室", "系统", "版本", "IP", "最后心跳"],
    list.map((a) => [
      badgeOnline(a.online),
      esc(a.student_name || "-"),
      esc(a.hostname),
      esc(a.classroom || "-"),
      `${esc(a.os)}/${esc(a.arch)}`,
      formatAgentVersionCell(a),
      esc(a.ip),
      fmtTime(a.last_seen),
    ]),
    list.map((a) => a.id)
  );
  if (selected) {
    el.querySelectorAll(`tr[data-id="${CSS.escape(selected)}"]`).forEach((tr) => tr.classList.add("active-row"));
  }
  bindAgentRows("#agentsList");
  bindUpgradeLatestButtons("#agentsList");
}

function bindAgentRows(root) {
  $$(`${root} tr[data-id]`).forEach((tr) => {
    tr.addEventListener("click", () => {
      setSelectedAgentId(tr.dataset.id);
      setView("agents");
      showAgent(tr.dataset.id);
    });
  });
}

async function showAgent(id) {
  await ensureUpdateVersions();
  const { agent, policy_id: policyID } = await api(`/api/agents/${encodeURIComponent(id)}`);
  const policyOpts = (state.policies.length ? state.policies : [{ id: "default", name: "默认课堂策略" }])
    .map((p) => `<option value="${escAttr(p.id)}" ${p.id === (policyID || "default") ? "selected" : ""}>${esc(p.name || p.id)}</option>`)
    .join("");
  const defaultTarget = state.latestAgentVersion || "";
  const upgradeOpts = versionOptionsHTML(defaultTarget);

  const listEl = $("#agentsList");
  if (listEl) {
    listEl.querySelectorAll("tr.active-row").forEach((tr) => tr.classList.remove("active-row"));
    listEl.querySelectorAll(`tr[data-id="${CSS.escape(id)}"]`).forEach((tr) => tr.classList.add("active-row"));
  }

  $("#agentDetail").innerHTML = `
    <div class="detail-block">
      <div class="detail-title">${esc(agent.student_name || agent.hostname)} ${badgeOnline(agent.online)}</div>
      <div class="kv">
        <span>Agent ID</span><span class="mono">${esc(agent.id)}</span>
        <span>主机名</span><span>${esc(agent.hostname)}</span>
        <span>系统</span><span>${esc(agent.os)} / ${esc(agent.arch)}</span>
        <span>IP</span><span>${esc(agent.ip)}</span>
        <span>版本</span><span>${formatAgentVersionCell(agent)}</span>
        <span>最后心跳</span><span>${fmtTime(agent.last_seen)}</span>
      </div>
      <div class="form-row" style="margin-top:12px">
        <label>学生姓名<input id="metaStudent" value="${escAttr(agent.student_name || "")}" /></label>
        <label>教室<input id="metaClassroom" value="${escAttr(agent.classroom || "")}" /></label>
      </div>
      <div class="form-row">
        <label>分配策略
          <select id="metaPolicy">${policyOpts}</select>
        </label>
        <label>升级到版本
          <select id="metaUpgradeVersion">${upgradeOpts}</select>
        </label>
      </div>
      <div class="actions">
        <button class="btn small primary" data-act="save-meta">保存信息</button>
        <button class="btn small primary" data-act="upgrade">升级到所选版本</button>
        <button class="btn small" data-act="monitor">查看监控</button>
        <button class="btn small" data-act="files">管理文件</button>
        <button class="btn small" data-act="terminal">打开终端</button>
        <button class="btn small" data-act="ping">Ping</button>
        <button class="btn small" data-act="message" title="发送给当前学生机">发消息</button>
        <button class="btn small" data-act="shutdown">关机</button>
        <button class="btn small" data-act="restart">重启</button>
        <button class="btn small danger" data-act="delete">移除</button>
      </div>
    </div>
  `;
  bindUpgradeLatestButtons("#agentDetail");

  $("#agentDetail").querySelectorAll("[data-act]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const act = btn.dataset.act;
      try {
        if (act === "delete") {
          if (!confirm("确认移除该学生机记录？")) return;
          await api(`/api/agents/${encodeURIComponent(id)}`, { method: "DELETE" });
          setSelectedAgentId(null);
          await loadAgents();
          return;
        }
        if (act === "monitor") {
          setSelectedAgentId(id);
          setView("monitor");
          return;
        }
        if (act === "files") {
          setSelectedAgentId(id);
          state.fsPath = "";
          state.fsAgentId = null;
          setView("files");
          return;
        }
        if (act === "terminal") {
          setSelectedAgentId(id);
          setView("terminal");
          return;
        }
        if (act === "save-meta") {
          const student = $("#metaStudent").value.trim();
          const classroom = $("#metaClassroom").value.trim();
          const policyId = $("#metaPolicy").value;
          await api(`/api/agents/${encodeURIComponent(id)}`, {
            method: "PATCH",
            body: JSON.stringify({ student_name: student, classroom }),
          });
          await api(`/api/agents/${encodeURIComponent(id)}/policy`, {
            method: "POST",
            body: JSON.stringify({ policy_id: policyId }),
          });
          alert("已保存学生信息与策略分配（后续心跳不会覆盖）");
          await showAgent(id);
          return;
        }
        if (act === "ping") {
          await sendCommand(id, "ping", {});
          alert("已下发 ping");
        }
        if (act === "message") {
          await sendTeacherMessage(id);
        }
        if (act === "upgrade") {
          const ver = ($("#metaUpgradeVersion")?.value || "").trim();
          if (!ver) {
            alert("请选择目标版本");
            return;
          }
          if (!confirm(`确认升级到 ${ver}？`)) return;
          await upgradeAgentToVersion(id, ver);
          alert(`已下发升级指令 → ${ver}`);
          return;
        }
        if (act === "shutdown") {
          if (!confirm("确认远程关机？")) return;
          await sendCommand(id, "shutdown", {});
          alert("已下发关机指令");
        }
        if (act === "restart") {
          if (!confirm("确认远程重启？")) return;
          await sendCommand(id, "restart", {});
          alert("已下发重启指令");
        }
      } catch (e) {
        alert(e.message);
      }
    });
  });
}

function formatPlatformLabel(plat) {
  const key = String(plat || "").trim().toLowerCase();
  const map = {
    "darwin-amd64": "macOS Intel",
    "darwin-arm64": "macOS Apple 芯片",
    "linux-amd64": "Linux",
    "linux-arm64": "Linux ARM",
    "windows-amd64": "Windows",
    "windows-arm64": "Windows ARM",
  };
  return map[key] || String(plat || "").trim();
}

function formatPlatforms(platforms) {
  const list = (platforms || []).map(formatPlatformLabel).filter(Boolean);
  return list.length ? list.join("、") : "—";
}

function normalizeUpdateNotes(notes) {
  const raw = String(notes || "").trim();
  if (!raw) return "—";
  const lower = raw.toLowerCase();
  if (
    lower.includes("deploy.sh") ||
    lower.includes("publish_update") ||
    /^published by\b/i.test(raw) ||
    /\bauto_update\b/i.test(raw)
  ) {
    return "—";
  }
  return raw;
}

async function loadUpdates() {
  const [{ versions, latest }, { agents }] = await Promise.all([
    api("/api/updates"),
    api("/api/agents"),
  ]);
  state.updateVersions = versions || [];
  state.latestAgentVersion = String(latest || "").trim();
  state.agents = agents || [];

  const hint = $("#updatesLatestHint");
  if (hint) {
    hint.textContent = state.latestAgentVersion || "暂无";
  }

  const list = state.updateVersions;
  $("#updatesList").innerHTML = list.length
    ? table(
        ["版本", "说明", "平台", "最新", "发布时间", "操作"],
        list.map((v) => {
          const ver = String(v.version || "").trim();
          const plats = formatPlatforms(v.platforms);
          const latestBadge = v.is_latest ? '<span class="badge ok">最新</span>' : '<span class="muted">—</span>';
          const actions = [
            v.is_latest
              ? ""
              : `<button class="btn small" data-set-latest="${escAttr(ver)}">设为最新</button>`,
            v.is_latest
              ? ""
              : `<button class="btn small danger" data-del-version="${escAttr(ver)}">删除</button>`,
          ]
            .filter(Boolean)
            .join(" ");
          return [
            `<span class="mono">${esc(ver)}</span>`,
            esc(normalizeUpdateNotes(v.notes)),
            esc(plats),
            latestBadge,
            fmtTime(v.created_at),
            actions || '<span class="muted">—</span>',
          ];
        })
      )
    : '<div class="empty">暂无已发布版本</div>';

  $("#updatesList").querySelectorAll("[data-set-latest]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const ver = btn.dataset.setLatest;
      if (!confirm(`将「当前最新」设为 ${ver}？`)) return;
      try {
        await api("/api/updates/latest", {
          method: "PUT",
          body: JSON.stringify({ version: ver }),
        });
        await loadUpdates();
      } catch (e) {
        alert(e.message);
      }
    });
  });
  $("#updatesList").querySelectorAll("[data-del-version]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const ver = btn.dataset.delVersion;
      if (!confirm(`确认删除版本 ${ver}？删除后不可恢复。`)) return;
      try {
        await api(`/api/updates/${encodeURIComponent(ver)}`, { method: "DELETE" });
        await loadUpdates();
      } catch (e) {
        alert(e.message);
      }
    });
  });

  renderAgentSelect("#updateAgentSelect");
  const verSel = $("#updateTargetVersion");
  if (verSel) verSel.innerHTML = versionOptionsHTML(state.latestAgentVersion || "");
}

async function loadMonitor() {
  const { agents } = await api("/api/agents");
  state.agents = agents || [];
  renderAgentSelect("#monitorAgentSelect");
  if (state.selectedAgentId && state.agents.some((a) => a.id === state.selectedAgentId)) {
    await showMonitor(state.selectedAgentId);
  } else {
    $("#monitorDetail").innerHTML = '<div class="empty">选择一台学生机查看监控数据</div>';
  }
}

function monitorAgentLabel(a) {
  const name = a.student_name || a.hostname || a.id;
  const parts = [name];
  if (a.student_name && a.hostname) parts.push(a.hostname);
  parts.push(a.online ? "在线" : "离线");
  const ver = String(a?.version || "").trim();
  parts.push(ver ? `v${ver}` : "版本未知");
  if (a.ip) parts.push(a.ip);
  if (a.classroom) parts.push(a.classroom);
  return parts.join(" · ");
}

async function fetchLatestAgentVersion() {
  try {
    const data = await api("/api/updates");
    const latest = String(data.latest || "").trim();
    state.updateVersions = data.versions || [];
    if (latest) return latest;
  } catch {
    /* fall through to compat endpoint */
  }
  try {
    const data = await api("/api/update/latest");
    return String(data.version || "").trim();
  } catch {
    return state.latestAgentVersion || "";
  }
}

function versionOptionsHTML(selected = "") {
  const list = state.updateVersions || [];
  const latest = state.latestAgentVersion || "";
  const opts = [`<option value="">选择版本…</option>`];
  for (const v of list) {
    const ver = String(v.version || "").trim();
    if (!ver) continue;
    const mark = v.is_latest || ver === latest ? "（最新）" : "";
    const sel = ver === selected ? " selected" : "";
    opts.push(`<option value="${escAttr(ver)}"${sel}>${esc(ver)}${mark}</option>`);
  }
  return opts.join("");
}

async function ensureUpdateVersions() {
  if (state.updateVersions && state.updateVersions.length) {
    if (!state.latestAgentVersion) {
      const hit = state.updateVersions.find((v) => v.is_latest);
      if (hit) state.latestAgentVersion = String(hit.version || "").trim();
    }
    return;
  }
  state.latestAgentVersion = await fetchLatestAgentVersion();
}

function agentVersionText(a) {
  const v = String(a?.version || "").trim();
  return v || "未知";
}

function normalizeVer(v) {
  v = String(v || "").trim();
  if (v.startsWith("v") || v.startsWith("V")) v = v.slice(1);
  return v;
}

function parseSemver(v) {
  v = normalizeVer(v);
  const cut = v.search(/[-+]/);
  if (cut >= 0) v = v.slice(0, cut);
  const parts = v.split(".");
  if (parts.length < 1 || parts.length > 3) return null;
  const out = [0, 0, 0];
  for (let i = 0; i < parts.length; i++) {
    if (!/^\d+$/.test(parts[i])) return null;
    out[i] = Number(parts[i]);
  }
  return out;
}

function versionLess(a, b) {
  a = normalizeVer(a);
  b = normalizeVer(b);
  if (!a) return !!b;
  if (!b) return false;
  const ap = parseSemver(a);
  const bp = parseSemver(b);
  if (ap && bp) {
    for (let i = 0; i < 3; i++) {
      if (ap[i] < bp[i]) return true;
      if (ap[i] > bp[i]) return false;
    }
    return false;
  }
  return a < b;
}

function formatAgentVersionCell(a) {
  const text = agentVersionText(a);
  const raw = String(a?.version || "").trim();
  const latest = state.latestAgentVersion || "";
  const outdated = raw && latest && versionLess(raw, latest);
  const badge = outdated
    ? ` <button type="button" class="badge warn linkish" data-upgrade-latest="${escAttr(a.id)}" data-upgrade-version="${escAttr(latest)}" title="升级到最新 ${escAttr(latest)}">有更新</button>`
    : "";
  return `<span class="mono">${esc(text)}</span>${badge}`;
}

async function upgradeAgentToVersion(agentId, version) {
  const ver = String(version || "").trim();
  if (!agentId) throw new Error("缺少学生机");
  if (!ver) throw new Error("缺少目标版本");
  await api(`/api/agents/${encodeURIComponent(agentId)}/update`, {
    method: "POST",
    body: JSON.stringify({ version: ver }),
  });
}

function bindUpgradeLatestButtons(rootSel) {
  const root = $(rootSel);
  if (!root) return;
  root.querySelectorAll("[data-upgrade-latest]").forEach((btn) => {
    btn.addEventListener("click", async (ev) => {
      ev.stopPropagation();
      const id = btn.dataset.upgradeLatest;
      const ver = btn.dataset.upgradeVersion || state.latestAgentVersion;
      if (!id || !ver) return;
      if (!confirm(`确认将学生机升级到最新版本 ${ver}？`)) return;
      try {
        await upgradeAgentToVersion(id, ver);
        alert(`已下发升级指令 → ${ver}`);
      } catch (e) {
        alert(e.message);
      }
    });
  });
}

function renderAgentSelect(selOrId) {
  const sel = typeof selOrId === "string" ? $(selOrId) : selOrId;
  if (!sel) return;
  const selected = state.selectedAgentId || "";
  const exists = state.agents.some((a) => a.id === selected);
  sel.innerHTML =
    `<option value="">选择学生机…</option>` +
    state.agents
      .map((a) => `<option value="${escAttr(a.id)}">${esc(monitorAgentLabel(a))}</option>`)
      .join("");
  sel.value = exists ? selected : "";
}

const MONITOR_TABS = [
  { id: "overview", label: "概览" },
  { id: "speed", label: "网速" },
  { id: "nics", label: "网卡" },
  { id: "disk", label: "磁盘" },
  { id: "procs", label: "进程" },
  { id: "nets", label: "网络" },
];

const PROC_READONLY_TIP = "当前为只读，关闭后可结束进程";

function setMonitorProcReadOnly(on) {
  state.monitorProcReadOnly = !!on;
  localStorage.setItem("proctor_monitor_proc_readonly", state.monitorProcReadOnly ? "1" : "0");
  applyMonitorProcReadOnlyUI();
  const host = document.querySelector("#monitorProcTable");
  if (host) host.innerHTML = renderMonitorProcRows(state.monitorProcs);
}

function applyMonitorProcReadOnlyUI() {
  const ro = !!state.monitorProcReadOnly;
  const root = $("#monitorDetail");
  const toggle = root?.querySelector("#monitorProcReadOnlyToggle");
  const label = root?.querySelector("#monitorProcReadOnlyLabel");
  if (toggle) toggle.checked = ro;
  label?.classList.toggle("is-on", ro);
  if (label) label.title = ro ? PROC_READONLY_TIP : "关闭只读后可结束进程";
}

function setMonitorTab(tabId) {
  const id = VALID_MONITOR_TABS.includes(tabId) ? tabId : "overview";
  state.monitorTab = id;
  localStorage.setItem("proctor_monitor_tab", id);
  const root = $("#monitorDetail");
  if (!root) return;
  root.querySelectorAll(".monitor-tab").forEach((btn) => {
    btn.classList.toggle("active", btn.dataset.tab === id);
  });
  root.querySelectorAll(".monitor-tab-panel").forEach((panel) => {
    panel.classList.toggle("hidden", panel.dataset.tab !== id);
  });
  // Filters + proc readonly toggle live outside tab panels so refreshes never destroy them.
  const procFilter = root.querySelector("#monitorProcFilter");
  const netFilter = root.querySelector("#monitorNetFilter");
  const procRO = root.querySelector("#monitorProcReadOnlyLabel");
  const filterBar = root.querySelector(".monitor-filter-bar");
  filterBar?.classList.toggle("hidden", id !== "procs" && id !== "nets");
  procFilter?.classList.toggle("hidden", id !== "procs");
  procRO?.classList.toggle("hidden", id !== "procs");
  netFilter?.classList.toggle("hidden", id !== "nets");
}

function matchesMonitorFilter(q, ...parts) {
  const needle = String(q || "").trim().toLowerCase();
  if (!needle) return true;
  return parts.some((p) => String(p ?? "").toLowerCase().includes(needle));
}

function renderMonitorProcRows(procs) {
  const filtered = (procs || []).filter((p) =>
    matchesMonitorFilter(state.monitorProcFilter, p.name, p.pid, p.username)
  );
  const ro = !!state.monitorProcReadOnly;
  return table(
    ["PID", "名称", "用户", "CPU", "内存", "RSS", "操作"],
    filtered.slice(0, 25).map((p) => [
      p.pid,
      `${esc(p.name)}${p.blacklisted ? ' <span class="badge critical">违规</span>' : ""}`,
      esc(p.username || "-"),
      `${num(p.cpu_percent)}%`,
      `${num(p.mem_percent)}%`,
      fmtBytes(p.mem_rss),
      ro
        ? `<span class="muted" title="${escAttr(PROC_READONLY_TIP)}">—</span>`
        : `<button class="btn small danger" data-kill="${p.pid}">结束</button>`,
    ])
  );
}

function renderMonitorNetRows(nets) {
  const filtered = (nets || []).filter((n) =>
    matchesMonitorFilter(state.monitorNetFilter, n.process, n.pid, n.status, n.laddr, n.raddr, n.remote_host, n.type)
  );
  return table(
    ["进程", "协议", "状态", "本地", "远端", "主机名"],
    filtered.slice(0, 30).map((n) => [
      esc(n.process || n.pid),
      esc(n.type || "-"),
      esc(n.status),
      esc(n.laddr),
      esc(n.raddr),
      esc(n.remote_host || "-"),
    ])
  );
}

function renderMonitorHeader(agent) {
  return `
    <div>
      <div class="detail-title">${esc(agent.student_name || agent.hostname)} ${badgeOnline(agent.online)}</div>
      <div class="kv">
        <span>主机名</span><span>${esc(agent.hostname)}</span>
        <span>系统</span><span>${esc(agent.os)} / ${esc(agent.arch)}</span>
        <span>IP</span><span>${esc(agent.ip)}</span>
        <span>最后心跳</span><span>${fmtTime(agent.last_seen)}</span>
      </div>
    </div>
    <div class="actions">
      <button class="btn small" data-act="goto-agent">设备管理</button>
      <button class="btn small" data-act="ping">Ping</button>
      <button class="btn small" data-act="message" title="发送给当前学生机">发消息</button>
    </div>
  `;
}

function renderMonitorTabBody({ activeTab, res, netIO, diskIO, disks, ifaces, procs, nets }) {
  return `
    <div class="monitor-tab-panel detail-block${activeTab === "overview" ? "" : " hidden"}" data-tab="overview" role="tabpanel">
      <div class="detail-title">系统资源</div>
      ${resourceBar("CPU", res.cpu_percent, res.cpu_count ? `${res.cpu_count} 核` : "")}
      ${resourceBar("内存", res.mem_percent, `${fmtBytes(res.mem_used)} / ${fmtBytes(res.mem_total)}`)}
      ${resourceBar("Swap", res.swap_percent, `${fmtBytes(res.swap_used)} / ${fmtBytes(res.swap_total)}`)}
      <div class="muted" style="margin-top:8px">Load ${num(res.load1)} / ${num(res.load5)} / ${num(res.load15)} · 运行 ${fmtUptime(res.uptime_seconds)}</div>
    </div>
    <div class="monitor-tab-panel detail-block${activeTab === "speed" ? "" : " hidden"}" data-tab="speed" role="tabpanel">
      <div class="detail-title">实时网速</div>
      <div class="metric-grid">
        <div class="metric-card down"><div class="label">下载</div><div class="value">${fmtSpeed(netIO.recv_bps)}</div><div class="hint">累计 ${fmtBytes(netIO.bytes_recv)}</div></div>
        <div class="metric-card up"><div class="label">上传</div><div class="value">${fmtSpeed(netIO.send_bps)}</div><div class="hint">累计 ${fmtBytes(netIO.bytes_sent)}</div></div>
        <div class="metric-card"><div class="label">收包</div><div class="value">${fmtPps(netIO.packets_recv_pps)}</div><div class="hint">累计 ${fmtNum(netIO.packets_recv)}</div></div>
        <div class="metric-card"><div class="label">发包</div><div class="value">${fmtPps(netIO.packets_sent_pps)}</div><div class="hint">累计 ${fmtNum(netIO.packets_sent)}</div></div>
    </div>
      <div class="muted" style="margin-top:10px">连接 ESTABLISHED ${netIO.conn_established || 0} · LISTEN ${netIO.conn_listen || 0} · 上报 ${netIO.conn_total || 0}</div>
    </div>
    <div class="monitor-tab-panel detail-block${activeTab === "nics" ? "" : " hidden"}" data-tab="nics" role="tabpanel">
      <div class="detail-title">网卡吞吐</div>
      ${table(
        ["网卡", "下载", "上传", "收字节", "发字节"],
        ifaces.slice(0, 8).map((n) => [
          esc(n.name),
          fmtSpeed(n.recv_bps),
          fmtSpeed(n.send_bps),
          fmtBytes(n.bytes_recv),
          fmtBytes(n.bytes_sent),
        ])
      )}
    </div>
    <div class="monitor-tab-panel detail-block${activeTab === "disk" ? "" : " hidden"}" data-tab="disk" role="tabpanel">
      <div class="detail-title">磁盘 IO</div>
      <div class="metric-grid">
        <div class="metric-card"><div class="label">磁盘读取</div><div class="value">${fmtSpeed(diskIO.read_bps)}</div><div class="hint">累计 ${fmtBytes(diskIO.read_bytes)}</div></div>
        <div class="metric-card"><div class="label">磁盘写入</div><div class="value">${fmtSpeed(diskIO.write_bps)}</div><div class="hint">累计 ${fmtBytes(diskIO.write_bytes)}</div></div>
        <div class="metric-card"><div class="label">读次数</div><div class="value">${fmtNum(diskIO.read_count)}</div><div class="hint">IO ops</div></div>
        <div class="metric-card"><div class="label">写次数</div><div class="value">${fmtNum(diskIO.write_count)}</div><div class="hint">IO ops</div></div>
      </div>
      <div class="detail-title" style="margin-top:16px">磁盘容量</div>
      ${disks.map((d) => resourceBar(d.mount_point, d.percent, `${fmtBytes(d.used)} / ${fmtBytes(d.total)} · ${esc(d.fs_type || "")}`)).join("") || '<div class="muted">无数据</div>'}
    </div>
    <div class="monitor-tab-panel detail-block${activeTab === "procs" ? "" : " hidden"}" data-tab="procs" role="tabpanel">
      <div class="detail-title">进程 Top</div>
      <div id="monitorProcTable">${renderMonitorProcRows(procs)}</div>
    </div>
    <div class="monitor-tab-panel detail-block${activeTab === "nets" ? "" : " hidden"}" data-tab="nets" role="tabpanel">
      <div class="detail-title">网络连接</div>
      <div id="monitorNetTable">${renderMonitorNetRows(nets)}</div>
    </div>
  `;
}

function ensureMonitorDelegates() {
  const root = $("#monitorDetail");
  if (!root || root.dataset.delegatesBound === "1") return;
  root.dataset.delegatesBound = "1";

  root.addEventListener("input", (e) => {
    const t = e.target;
    if (!(t instanceof HTMLInputElement)) return;
    if (t.id === "monitorProcFilter") {
      state.monitorProcFilter = t.value;
      localStorage.setItem("proctor_monitor_proc_filter", state.monitorProcFilter);
      const host = root.querySelector("#monitorProcTable");
      if (host) host.innerHTML = renderMonitorProcRows(state.monitorProcs);
      return;
    }
    if (t.id === "monitorNetFilter") {
      state.monitorNetFilter = t.value;
      localStorage.setItem("proctor_monitor_net_filter", state.monitorNetFilter);
      const host = root.querySelector("#monitorNetTable");
      if (host) host.innerHTML = renderMonitorNetRows(state.monitorNets);
    }
  });

  root.addEventListener("change", (e) => {
    const t = e.target;
    if (!(t instanceof HTMLInputElement)) return;
    if (t.id === "monitorProcReadOnlyToggle") {
      setMonitorProcReadOnly(!!t.checked);
    }
  });

  root.addEventListener("click", async (e) => {
    const tabBtn = e.target.closest(".monitor-tab");
    if (tabBtn) {
      setMonitorTab(tabBtn.dataset.tab);
      return;
    }

    const shell = root.querySelector("[data-monitor-shell]");
    const agentId = shell?.dataset.agentId;
    if (!agentId) return;

    const killBtn = e.target.closest("[data-kill]");
    if (killBtn) {
      if (state.monitorProcReadOnly) {
        alert(PROC_READONLY_TIP);
        return;
      }
      if (!confirm(`结束进程 PID ${killBtn.dataset.kill}？`)) return;
      try {
        await sendCommand(agentId, "kill_process", { pid: killBtn.dataset.kill });
        alert("已下发结束进程指令");
      } catch (err) {
        alert(err.message);
      }
      return;
    }

    const actBtn = e.target.closest("[data-act]");
    if (!actBtn) return;
    const act = actBtn.dataset.act;
    try {
      if (act === "goto-agent") {
        setSelectedAgentId(agentId);
        setView("agents");
        return;
      }
      if (act === "ping") {
        await sendCommand(agentId, "ping", {});
        alert("已下发 ping");
      }
      if (act === "message") {
        await sendTeacherMessage(agentId);
      }
    } catch (err) {
      alert(err.message);
    }
  });
}

async function showMonitor(id) {
  const { agent, heartbeat } = await api(`/api/agents/${encodeURIComponent(id)}`);
  const hb = heartbeat || {};
  const res = hb.resources || {};
  const netIO = hb.net_io || {};
  const diskIO = hb.disk_io || {};
  const disks = hb.disks || [];
  const procs = hb.processes || [];
  const nets = hb.networks || [];
  const ifaces = netIO.interfaces || [];
  const activeTab = VALID_MONITOR_TABS.includes(state.monitorTab) ? state.monitorTab : "overview";
  state.monitorTab = activeTab;
  state.monitorProcs = procs;
  state.monitorNets = nets;

  const root = $("#monitorDetail");
  if (!root) return;
  ensureMonitorDelegates();

  const shell = root.querySelector("[data-monitor-shell]");
  // Same agent: refresh data regions only — filter inputs stay mounted (outside #monitorTabBody).
  if (shell && shell.dataset.agentId === id) {
    const header = shell.querySelector("#monitorHeader");
    if (header) header.innerHTML = renderMonitorHeader(agent);
    const body = shell.querySelector("#monitorTabBody");
    if (body) {
      body.innerHTML = renderMonitorTabBody({ activeTab, res, netIO, diskIO, disks, ifaces, procs, nets });
    }
    setMonitorTab(activeTab);
    return;
  }

  const procFilter = state.monitorProcFilter || "";
  const netFilter = state.monitorNetFilter || "";
  const procRO = !!state.monitorProcReadOnly;
  const tabsHtml = MONITOR_TABS.map(
    (t) =>
      `<button type="button" class="monitor-tab${t.id === activeTab ? " active" : ""}" data-tab="${t.id}">${t.label}</button>`
  ).join("");
  const showProcFilter = activeTab === "procs";
  const showNetFilter = activeTab === "nets";
  const showFilterBar = showProcFilter || showNetFilter;

  root.innerHTML = `
    <div data-monitor-shell data-agent-id="${escAttr(id)}">
      <div class="monitor-header" id="monitorHeader">${renderMonitorHeader(agent)}</div>
      <div class="monitor-tabs" role="tablist">${tabsHtml}</div>
      <div class="monitor-filter-bar${showFilterBar ? "" : " hidden"}">
        <input id="monitorProcFilter" class="monitor-filter${showProcFilter ? "" : " hidden"}" type="search"
          placeholder="筛选进程名 / PID / 用户" value="${escAttr(procFilter)}" autocomplete="off" />
        <label class="fs-readonly-toggle${showProcFilter ? "" : " hidden"}${procRO ? " is-on" : ""}" id="monitorProcReadOnlyLabel"
          title="${escAttr(procRO ? PROC_READONLY_TIP : "关闭只读后可结束进程")}">
          <input id="monitorProcReadOnlyToggle" type="checkbox"${procRO ? " checked" : ""} />
          <span class="fs-readonly-switch" aria-hidden="true"></span>
          <span class="fs-readonly-text">只读</span>
        </label>
        <input id="monitorNetFilter" class="monitor-filter${showNetFilter ? "" : " hidden"}" type="search"
          placeholder="筛选进程 / 状态 / 地址 / 主机名" value="${escAttr(netFilter)}" autocomplete="off" />
      </div>
      <div id="monitorTabBody" class="monitor-tab-panels">
        ${renderMonitorTabBody({ activeTab, res, netIO, diskIO, disks, ifaces, procs, nets })}
      </div>
    </div>
  `;
  applyMonitorProcReadOnlyUI();
}

async function loadFiles() {
  const { agents } = await api("/api/agents");
  state.agents = agents || [];
  renderAgentSelect("#filesAgentSelect");
  if (!state.selectedAgentId || !state.agents.some((a) => a.id === state.selectedAgentId)) {
    resetFilesEmpty();
    return;
  }
  // Agent changed (or forced reset) — open that machine's roots.
  if (state.fsAgentId !== state.selectedAgentId) {
    state.fsAgentId = state.selectedAgentId;
    state.fsPath = "";
    $("#fsPreview")?.classList.add("hidden");
    if (!state.fsBusy) await fsBrowse("", { silent: true });
    return;
  }
  // Initial open only — avoid auto-refresh interrupting browse/upload.
  if (!$("#fsListing .fs-name") && !$("#fsListing table") && !state.fsBusy) {
    await fsBrowse(state.fsPath || "", { silent: true });
  }
}

function resetFilesEmpty() {
  state.fsPath = "";
  state.fsAgentId = null;
  state.fsEntries = [];
  $("#fsBreadcrumb").textContent = "未选择学生机";
  $("#fsListing").innerHTML = '<div class="empty">请先选择学生机</div>';
  $("#fsPreview")?.classList.add("hidden");
  setFSStatus("");
  applyFSReadOnlyUI();
}

const FS_WRITE_OPS = new Set(["write", "mkdir", "delete", "rename"]);
const FS_READONLY_TIP = "当前为只读，关闭后可修改";

function setFSReadOnly(on) {
  state.fsReadOnly = !!on;
  localStorage.setItem("proctor_fs_readonly", state.fsReadOnly ? "1" : "0");
  applyFSReadOnlyUI();
  if (state.fsEntries.length) renderFSEntries(state.fsEntries);
}

function applyFSReadOnlyUI() {
  const ro = !!state.fsReadOnly;
  const toggle = $("#fsReadOnlyToggle");
  const label = $("#fsReadOnlyLabel");
  const hint = $("#fsReadOnlyHint");
  const mkdirBtn = $("#fsMkdirBtn");
  const uploadBtn = $("#fsUploadBtn");
  const uploadInput = $("#fsUploadInput");

  if (toggle) toggle.checked = ro;
  label?.classList.toggle("is-on", ro);
  if (label) label.title = ro ? FS_READONLY_TIP : "关闭只读后可上传、新建、重命名、删除";
  if (hint) {
    hint.textContent = FS_READONLY_TIP;
    hint.classList.toggle("hidden", !ro);
  }

  if (mkdirBtn) {
    mkdirBtn.disabled = ro;
    mkdirBtn.classList.toggle("is-disabled", ro);
    mkdirBtn.title = ro ? FS_READONLY_TIP : "";
  }
  if (uploadBtn) {
    uploadBtn.classList.toggle("is-disabled", ro);
    uploadBtn.setAttribute("aria-disabled", ro ? "true" : "false");
    uploadBtn.title = ro ? FS_READONLY_TIP : "";
  }
  if (uploadInput) uploadInput.disabled = ro;
}

function ensureFSWritable() {
  if (!state.fsReadOnly) return true;
  alert(FS_READONLY_TIP);
  return false;
}

function setFSStatus(msg) {
  const el = $("#fsStatus");
  if (el) el.textContent = msg || "";
}

function fsJobWaitMessage(status) {
  switch (String(status || "").toLowerCase()) {
    case "delivered":
      return "学生机处理中…";
    case "pending":
    default:
      return "等待学生机响应…";
  }
}

async function enqueueFS(op, path = "", extra = {}) {
  if (!state.selectedAgentId) throw new Error("请先选择学生机");
  if (FS_WRITE_OPS.has(op) && state.fsReadOnly) {
    throw new Error(FS_READONLY_TIP);
  }
  const { job } = await api(`/api/agents/${encodeURIComponent(state.selectedAgentId)}/fs`, {
    method: "POST",
    body: JSON.stringify({ op, path, dest: extra.dest || "", content: extra.content || "" }),
  });
  return waitFSJob(job.id, { silent: !!extra.silent, statusMsg: extra.statusMsg });
}

async function waitFSJob(jobId, opts = {}) {
  const timeoutMs = opts.timeoutMs || 45000;
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const { job } = await api(`/api/fs/jobs/${encodeURIComponent(jobId)}`);
    if (job.status === "done") return job;
    if (job.status === "failed") throw new Error(job.error || "远程文件操作失败");
    // One Chinese line only — never append raw job.status (pending/delivered).
    if (!opts.silent) {
      setFSStatus(opts.statusMsg || fsJobWaitMessage(job.status));
    }
    await sleep(800);
  }
  throw new Error("等待超时，请确认学生机在线且采集间隔较短");
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

async function fsBrowse(path, opts = {}) {
  if (!state.selectedAgentId || state.fsBusy) return;
  state.fsBusy = true;
  const loadingMsg = "加载中…";
  setFSStatus(opts.silent ? "" : loadingMsg);
  $("#fsPreview")?.classList.add("hidden");
  try {
    const op = path ? "list" : "roots";
    const job = await enqueueFS(op, path || "", {
      silent: !!opts.silent,
      statusMsg: opts.silent ? "" : loadingMsg,
    });
    const result = job.result || {};
    state.fsPath = result.path || path || "";
    $("#fsBreadcrumb").textContent = state.fsPath || "根目录 / 快捷入口";
    const entries = result.entries || [];
    state.fsEntries = entries;
    if (!entries.length) {
      $("#fsListing").innerHTML = '<div class="empty">空目录</div>';
    } else {
      renderFSEntries(entries);
    }
    applyFSReadOnlyUI();
    setFSStatus(`共 ${entries.length} 项`);
  } catch (e) {
    if (!opts.silent) alert(e.message);
    setFSStatus(e.message);
  } finally {
    state.fsBusy = false;
  }
}

function renderFSEntries(entries) {
  state.fsEntries = entries || [];
  const ro = !!state.fsReadOnly;
  $("#fsListing").innerHTML = table(
    ["名称", "类型", "大小", "修改时间", "操作"],
    entries.map((e) => [
      `<span class="fs-name"><span class="fs-icon ${e.is_dir ? "dir" : ""}">${e.is_dir ? "D" : "F"}</span><a href="#" data-open="${escAttr(e.path)}" data-dir="${e.is_dir ? "1" : "0"}" data-name="${escAttr(e.name)}">${esc(e.name)}</a></span>`,
      e.is_dir ? "文件夹" : "文件",
      e.is_dir ? "-" : fmtBytes(e.size),
      fmtTime(e.mod_time),
      [
        e.is_dir
          ? `<button class="btn small" data-open="${escAttr(e.path)}" data-dir="1">打开</button>`
          : `<button class="btn small" data-read="${escAttr(e.path)}" data-name="${escAttr(e.name)}">查看/下载</button>`,
        ro
          ? ""
          : `<button class="btn small" data-rename="${escAttr(e.path)}" data-name="${escAttr(e.name)}">重命名</button>`,
        ro
          ? ""
          : `<button class="btn small danger" data-del="${escAttr(e.path)}" data-dir="${e.is_dir ? "1" : "0"}">删除</button>`,
      ].filter(Boolean).join(" "),
    ])
  );
  $$("#fsListing [data-open]").forEach((el) => {
    el.addEventListener("click", (ev) => {
      ev.preventDefault();
      if (el.dataset.dir === "1") fsBrowse(el.dataset.open);
      else fsReadFile(el.dataset.open, el.dataset.name || "");
    });
  });
  $$("#fsListing [data-read]").forEach((el) => {
    el.addEventListener("click", () => fsReadFile(el.dataset.read, el.dataset.name || ""));
  });
  $$("#fsListing [data-rename]").forEach((el) => {
    el.addEventListener("click", () => fsRename(el.dataset.rename, el.dataset.name || ""));
  });
  $$("#fsListing [data-del]").forEach((el) => {
    el.addEventListener("click", () => fsDelete(el.dataset.del, el.dataset.dir === "1"));
  });
}

async function fsReadFile(path, name) {
  if (state.fsBusy) return;
  state.fsBusy = true;
  setFSStatus("读取文件…");
  try {
    const job = await enqueueFS("read", path, { statusMsg: "读取文件…" });
    const result = job.result || {};
    const bytes = base64ToBytes(result.content || "");
    const preview = $("#fsPreview");
    preview.classList.remove("hidden");
    const text = tryDecodeText(bytes);
    if (text != null) {
      preview.innerHTML = `<div class="detail-title">${esc(name || result.name || path)} ${result.truncated ? '<span class="badge warn">已截断</span>' : ""}</div><pre>${esc(text)}</pre>
        <div class="actions"><button class="btn small" id="fsDownloadBtn">下载原文件</button></div>`;
    } else {
      preview.innerHTML = `<div class="detail-title">${esc(name || result.name || path)} <span class="badge off">二进制</span> ${result.truncated ? '<span class="badge warn">已截断</span>' : ""}</div>
        <div class="muted">大小 ${fmtBytes(result.size)}，可下载到本地查看</div>
        <div class="actions"><button class="btn small" id="fsDownloadBtn">下载</button></div>`;
    }
    $("#fsDownloadBtn")?.addEventListener("click", () => downloadBytes(bytes, name || result.name || "download.bin"));
    setFSStatus(result.truncated ? "已读取（超过 4MB 已截断）" : "已读取");
  } catch (e) {
    alert(e.message);
    setFSStatus(e.message);
  } finally {
    state.fsBusy = false;
  }
}

async function fsDelete(path, isDir) {
  if (!ensureFSWritable()) return;
  if (!confirm(`确认删除${isDir ? "空文件夹" : "文件"}？\n${path}`)) return;
  try {
    await enqueueFS("delete", path);
    await fsBrowse(state.fsPath);
  } catch (e) {
    alert(e.message);
  }
}

async function fsRename(path, oldName) {
  if (!ensureFSWritable()) return;
  const next = prompt("新名称", oldName);
  if (!next || next === oldName) return;
  const dest = joinPath(parentPath(path), next);
  try {
    await enqueueFS("rename", path, { dest });
    await fsBrowse(state.fsPath);
  } catch (e) {
    alert(e.message);
  }
}

async function fsMkdir() {
  if (!ensureFSWritable()) return;
  const name = prompt("新建文件夹名称");
  if (!name) return;
  if (!state.fsPath) {
    alert("请先进入一个目录再新建");
    return;
  }
  try {
    await enqueueFS("mkdir", joinPath(state.fsPath, name));
    await fsBrowse(state.fsPath);
  } catch (e) {
    alert(e.message);
  }
}

async function fsUpload(file) {
  if (!file) return;
  if (!ensureFSWritable()) return;
  if (!state.fsPath) {
    alert("请先进入一个目录再上传");
    return;
  }
  if (file.size > 4 * 1024 * 1024) {
    alert("上传限制 4MB");
    return;
  }
  const uploadingMsg = `上传 ${file.name}…`;
  setFSStatus(uploadingMsg);
  try {
    const buf = await file.arrayBuffer();
    const content = bytesToBase64(new Uint8Array(buf));
    await enqueueFS("write", joinPath(state.fsPath, file.name), { content, statusMsg: uploadingMsg });
    await fsBrowse(state.fsPath);
    setFSStatus(`已上传 ${file.name}`);
  } catch (e) {
    alert(e.message);
    setFSStatus(e.message);
  }
}

function parentPath(path) {
  if (!path) return "";
  const norm = path.replace(/\\/g, "/").replace(/\/+$/, "");
  const idx = norm.lastIndexOf("/");
  if (idx <= 0) {
    // Windows drive root like C:
    if (/^[A-Za-z]:$/.test(norm)) return norm + "/";
    return "";
  }
  // Keep Windows drive root "C:/"
  const parent = norm.slice(0, idx);
  if (/^[A-Za-z]:$/.test(parent)) return parent + "/";
  return parent || "/";
}

function joinPath(base, name) {
  if (!base) return name;
  const sep = base.includes("\\") && !base.includes("/") ? "\\" : "/";
  if (base.endsWith("/") || base.endsWith("\\")) return base + name;
  return base + sep + name;
}

function base64ToBytes(b64) {
  const bin = atob(b64 || "");
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

function bytesToBase64(bytes) {
  let s = "";
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    s += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(s);
}

function tryDecodeText(bytes) {
  if (!bytes || !bytes.length) return "";
  // Heuristic: reject if too many NUL / high control chars
  let bad = 0;
  const sample = Math.min(bytes.length, 800);
  for (let i = 0; i < sample; i++) {
    const c = bytes[i];
    if (c === 0) return null;
    if (c < 7 || (c > 14 && c < 32 && c !== 9 && c !== 10 && c !== 13)) bad++;
  }
  if (bad / sample > 0.05) return null;
  try {
    return new TextDecoder("utf-8", { fatal: false }).decode(bytes);
  } catch {
    return null;
  }
}

function downloadBytes(bytes, filename) {
  const blob = new Blob([bytes]);
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename || "download.bin";
  a.click();
  URL.revokeObjectURL(url);
}

async function loadTerminal() {
  const { agents } = await api("/api/agents");
  state.agents = agents || [];
  renderAgentSelect("#termAgentSelect");
  syncTermUI();
}

function syncTermUI() {
  const id = state.selectedAgentId;
  const agent = id ? state.agents.find((a) => a.id === id) : null;
  const has = !!agent;
  $("#termEmpty")?.classList.toggle("hidden", has);
  $("#termWorkspace")?.classList.toggle("hidden", !has);
  if (!has) {
    syncTermModeFields();
    return;
  }
  if ($("#sshHost") && !$("#sshHost").value) {
    $("#sshHost").value = agent.ip || "";
  }
  const hint = $("#termHint");
  if (hint) {
    const label = agent.student_name || agent.hostname || id;
    hint.textContent = `已选择 ${label}，选择模式后点击连接。Agent 模式最多等待一次心跳周期。`;
  }
  syncTermModeFields();
  ensureTerminal();
  requestAnimationFrame(() => {
    state.termFit?.fit();
    sendTermResize();
  });
}

function syncTermModeFields() {
  const mode = $("#termMode")?.value || "agent";
  $("#sshFields")?.classList.toggle("hidden", mode !== "ssh");
}

function ensureTerminal() {
  if (state.term || typeof Terminal === "undefined") return;
  state.term = new Terminal({
    cursorBlink: true,
    fontSize: 13,
    fontFamily: "IBM Plex Mono, Menlo, Monaco, Consolas, monospace",
    theme: { background: "#0b0f14", foreground: "#e8eef5", cursor: "#5eb0f0", selectionBackground: "#2a3f55" },
  });
  if (typeof FitAddon !== "undefined") {
    state.termFit = new FitAddon.FitAddon();
    state.term.loadAddon(state.termFit);
  }
  state.term.open($("#termContainer"));
  state.termFit?.fit();
  state.term.writeln("Proctor 远程终端就绪。选择学生机并连接。");
  state.term.onData((data) => {
    if (state.termWS && state.termWS.readyState === WebSocket.OPEN) {
      state.termWS.send(JSON.stringify({ type: "input", data }));
    }
  });
  window.addEventListener("resize", () => {
    if (state.view === "terminal") {
      state.termFit?.fit();
      sendTermResize();
    }
  });
}

function sendTermResize() {
  if (!state.term || !state.termWS || state.termWS.readyState !== WebSocket.OPEN) return;
  state.termWS.send(JSON.stringify({
    type: "resize",
    cols: state.term.cols,
    rows: state.term.rows,
  }));
}

function termDisconnect() {
  if (state.termWS) {
    try { state.termWS.close(); } catch {}
    state.termWS = null;
  }
  state.termAgent = null;
}

function termConnect() {
  if (!state.selectedAgentId) {
    alert("请先选择学生机");
    return;
  }
  if (typeof Terminal === "undefined") {
    alert("终端组件加载失败，请检查网络（需访问 jsDelivr CDN）");
    return;
  }
  ensureTerminal();
  termDisconnect();
  state.term.clear();
  state.termFit?.fit();

  const mode = $("#termMode").value;
  const proto = location.protocol === "https:" ? "wss" : "ws";
  const qs = new URLSearchParams({ token: state.token });
  const wsURL = `${proto}://${location.host}/api/agents/${encodeURIComponent(state.selectedAgentId)}/shell?${qs}`;
  const ws = new WebSocket(wsURL);
  state.termWS = ws;
  state.termAgent = state.selectedAgentId;
  state.term.writeln(`正在连接 (${mode})…`);

  ws.onopen = () => {
    const payload = {
      mode,
      cols: state.term.cols,
      rows: state.term.rows,
      host: $("#sshHost")?.value?.trim() || "",
      port: Number($("#sshPort")?.value || 22),
      user: $("#sshUser")?.value?.trim() || "",
      password: $("#sshPassword")?.value || "",
    };
    ws.send(JSON.stringify(payload));
  };
  ws.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    if (msg.type === "output") state.term.write(msg.data || "");
    if (msg.type === "ready") {
      state.term.writeln(`\r\n\x1b[32m${msg.message || "已连接"}\x1b[0m\r\n`);
      sendTermResize();
    }
    if (msg.type === "error") {
      state.term.writeln(`\r\n\x1b[31m${msg.message || "错误"}\x1b[0m\r\n`);
    }
  };
  ws.onclose = () => {
    state.term?.writeln("\r\n\x1b[33m[连接已关闭]\x1b[0m\r\n");
    if (state.termWS === ws) state.termWS = null;
  };
  ws.onerror = () => {
    state.term?.writeln("\r\n\x1b[31m[WebSocket 错误]\x1b[0m\r\n");
  };
}

function resourceBar(label, percent, extra = "") {
  const p = Number(percent) || 0;
  return `<div style="margin-bottom:10px">
    <div style="display:flex;justify-content:space-between;gap:8px">
      <span>${esc(label)}</span><span class="muted">${p.toFixed(1)}% ${esc(extra)}</span>
    </div>
    <div class="meter ${meterClass(p)}"><i style="width:${Math.min(100, p)}%"></i></div>
  </div>`;
}

async function sendCommand(agentId, type, payload) {
  return api(`/api/agents/${encodeURIComponent(agentId)}/command`, {
    method: "POST",
    body: JSON.stringify({ type, payload }),
  });
}

function agentDisplayName(agentOrId) {
  if (agentOrId && typeof agentOrId === "object") {
    return agentOrId.student_name || agentOrId.hostname || agentOrId.id || "-";
  }
  const id = String(agentOrId || "");
  const a = (state.agents || []).find((x) => x.id === id);
  return a ? (a.student_name || a.hostname || a.id) : (id || "-");
}

function resolveMessageTargets(scope, currentAgentId) {
  const agents = state.agents || [];
  if (scope === "current") {
    return currentAgentId ? [currentAgentId] : [];
  }
  if (scope === "online") {
    return agents.filter((a) => a.online).map((a) => a.id);
  }
  return [];
}

function updateBroadcastTargetHint() {
  const dialogHint = $("#messageDialogHint");
  if (!dialogHint) return;
  const onlineCount = resolveMessageTargets("online", null).length;
  dialogHint.textContent = onlineCount
    ? `将发送给全部在线学生机（当前在线 ${onlineCount} 台）`
    : "当前没有在线学生机";
}

function openMessageDialog(currentAgentId, opts = {}) {
  const dialog = $("#messageDialog");
  if (!dialog) return Promise.resolve(null);

  const broadcast = opts.mode === "broadcast";
  const titleEl = $("#messageDialogTitle");
  const hintEl = $("#messageDialogHint");
  const sendBtn = $("#messageDialogSend");
  const textEl = $("#messageDialogText");

  if (titleEl) titleEl.textContent = broadcast ? "广播" : "发消息";
  if (sendBtn) sendBtn.textContent = broadcast ? "广播" : "发送";

  if (broadcast) {
    updateBroadcastTargetHint();
  } else if (hintEl) {
    hintEl.textContent = currentAgentId
      ? `发送给：${agentDisplayName(currentAgentId)}`
      : "请先选择一台学生机";
  }

  if (textEl) textEl.value = "";

  dialog.classList.remove("hidden");
  dialog.setAttribute("aria-hidden", "false");
  textEl?.focus();

  return new Promise((resolve) => {
    const cleanup = () => {
      dialog.classList.add("hidden");
      dialog.setAttribute("aria-hidden", "true");
      dialog.removeEventListener("click", onClick);
      document.removeEventListener("keydown", onKey);
    };
    const finish = (value) => {
      cleanup();
      resolve(value);
    };
    const onClick = (e) => {
      if (e.target.closest("[data-message-cancel]")) {
        finish(null);
        return;
      }
      if (e.target.closest("#messageDialogSend")) {
        const text = String(textEl?.value || "").trim();
        if (!text) {
          alert(broadcast ? "请输入广播内容" : "请输入消息内容");
          textEl?.focus();
          return;
        }
        if (broadcast) {
          const targets = resolveMessageTargets("online", null);
          if (!targets.length) {
            alert("没有可发送的在线学生机");
            return;
          }
          finish({ text, scope: "online", targets, broadcast: true });
          return;
        }
        if (!currentAgentId) {
          alert("请先选择一台学生机");
          return;
        }
        finish({
          text,
          scope: "current",
          targets: [currentAgentId],
          broadcast: false,
        });
      }
    };
    const onKey = (e) => {
      if (e.key === "Escape") finish(null);
    };
    dialog.addEventListener("click", onClick);
    document.addEventListener("keydown", onKey);
  });
}

async function sendTeacherMessage(currentAgentId, opts = {}) {
  const choice = await openMessageDialog(currentAgentId, opts);
  if (!choice) return;
  const { text, targets, broadcast } = choice;
  let ok = 0;
  const errors = [];
  await Promise.all(
    targets.map(async (id) => {
      try {
        await sendCommand(id, "message", { text, reply: "true" });
        ok += 1;
      } catch (e) {
        errors.push(`${agentDisplayName(id)}: ${e.message}`);
      }
    })
  );
  const fail = errors.length;
  const action = broadcast ? "广播" : "消息";
  const base =
    `${action}完成：成功 ${ok} 台，失败 ${fail} 台（共 ${targets.length} 台）。` +
    `学生机将弹出确认框，可回复；详情见「指令」页`;
  alert(fail ? `${base}\n失败明细：\n${errors.slice(0, 8).join("\n")}${fail > 8 ? `\n…另有 ${fail - 8} 台` : ""}` : base);
}

async function broadcastTeacherMessage() {
  try {
    const { agents } = await api("/api/agents");
    state.agents = agents || [];
  } catch (_) {
    /* 沿用现有缓存列表 */
  }
  return sendTeacherMessage(state.selectedAgentId || null, { mode: "broadcast" });
}

function matchesAlertFilter(q, alert) {
  const needle = String(q || "").trim().toLowerCase();
  if (!needle) return true;
  const timeText = fmtTime(alert.created_at);
  return matchesMonitorFilter(
    needle,
    alert.level,
    alert.category,
    alert.agent_id,
    alert.message,
    alert.detail,
    timeText,
    alert.acked ? "已确认" : "确认"
  );
}

function renderAlertRows(alerts) {
  const filtered = (alerts || []).filter((a) => matchesAlertFilter(state.alertFilter, a));
  return table(
    ["级别", "类别", "学生机", "内容", "时间", "操作"],
    filtered.map((a) => [
      `<span class="badge ${a.level === "critical" ? "critical" : "warn"}">${esc(a.level)}</span>`,
      esc(a.category),
      `<span class="mono">${esc(a.agent_id)}</span>`,
      `${esc(a.message)}${a.detail ? `<div class="muted mono">${esc(a.detail)}</div>` : ""}`,
      fmtTime(a.created_at),
      a.acked ? '<span class="muted">已确认</span>' : `<button class="btn small" data-ack="${esc(a.id)}">确认</button>`,
    ])
  );
}

function bindAlertAckButtons() {
  $$("#alertList [data-ack]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      await api(`/api/alerts/${encodeURIComponent(btn.dataset.ack)}/ack`, { method: "POST" });
      await loadAlerts();
    });
  });
}

async function loadAlerts() {
  const [alertsRes, retention] = await Promise.all([
    api("/api/alerts"),
    api("/api/alerts/retention"),
  ]);
  state.alerts = alertsRes.alerts || [];
  const input = $("#alertRetentionLimit");
  if (input && document.activeElement !== input) {
    input.value = retention.limit ?? alertsRes.limit ?? 200;
    if (retention.min != null) input.min = retention.min;
    if (retention.max != null) input.max = retention.max;
  }
  const filterInput = $("#alertFilter");
  if (filterInput && document.activeElement !== filterInput) {
    filterInput.value = state.alertFilter || "";
  }
  $("#alertList").innerHTML = renderAlertRows(state.alerts);
  bindAlertAckButtons();
}

async function saveAlertRetention() {
  const input = $("#alertRetentionLimit");
  const limit = Number(input?.value);
  if (!Number.isFinite(limit) || !Number.isInteger(limit)) {
    throw new Error("请输入有效的整数条数");
  }
  const min = Number(input.min) || 10;
  const max = Number(input.max) || 10000;
  if (limit < min || limit > max) {
    throw new Error(`每台机器保留条数需在 ${min}–${max} 之间`);
  }
  const res = await api("/api/alerts/retention", {
    method: "PUT",
    body: JSON.stringify({ limit }),
  });
  const deleted = Number(res.deleted) || 0;
  alert(
    deleted > 0
      ? `已保存，并按每台机器清理超出记录（共 ${deleted} 条）`
      : "已保存（已按每台机器清理超出记录）"
  );
  await loadAlerts();
}

async function loadPolicies() {
  const { policies } = await api("/api/policies");
  state.policies = policies || [];
  if (!state.policies.length) {
    $("#policyList").innerHTML = '<div class="empty">暂无策略</div>';
    $("#policyForm").innerHTML = "";
    return;
  }
  if (!state.policies.find((p) => p.id === state.selectedPolicyId)) {
    state.selectedPolicyId = state.policies[0].id;
  }
  $("#policyList").innerHTML = table(
    ["名称", "启用", "模式"],
    state.policies.map((p) => [
      esc(p.name || p.id),
      p.enabled ? '<span class="badge ok">启用</span>' : '<span class="badge off">停用</span>',
      p.process_whitelist_mode ? "白名单" : "黑名单",
    ]),
    state.policies.map((p) => p.id)
  );
  $$("#policyList tr[data-id]").forEach((tr) => {
    tr.classList.toggle("active-row", tr.dataset.id === state.selectedPolicyId);
    tr.addEventListener("click", () => {
      state.selectedPolicyId = tr.dataset.id;
      renderPolicyForm();
      $$("#policyList tr[data-id]").forEach((x) => x.classList.toggle("active-row", x.dataset.id === state.selectedPolicyId));
    });
  });
  renderPolicyForm();
}

function renderPolicyForm() {
  const p = state.policies.find((x) => x.id === state.selectedPolicyId);
  state.policy = p;
  if (!p) {
    $("#policyForm").innerHTML = '<div class="empty">选择策略</div>';
    return;
  }
  $("#policyForm").innerHTML = `
    <div class="form-row">
      <label>名称<input name="name" value="${escAttr(p.name || "")}" /></label>
      <label>启用
        <select name="enabled"><option value="true" ${p.enabled ? "selected" : ""}>是</option><option value="false" ${!p.enabled ? "selected" : ""}>否</option></select>
      </label>
    </div>
    <div class="form-row">
      <label>采集间隔(秒)<input name="collect_interval_sec" type="number" value="${p.collect_interval_sec || 15}" /></label>
      <label>上报进程数<input name="report_top_n_processes" type="number" value="${p.report_top_n_processes || 30}" /></label>
    </div>
    <div class="form-row">
      <label>CPU 告警阈值%<input name="max_cpu_percent" type="number" value="${p.max_cpu_percent || 95}" /></label>
      <label>内存告警阈值%<input name="max_mem_percent" type="number" value="${p.max_mem_percent || 95}" /></label>
    </div>
    <div class="form-row">
      <label>磁盘告警阈值%<input name="max_disk_percent" type="number" value="${p.max_disk_percent || 92}" /></label>
      <label>自动结束违规进程
        <select name="kill_blacklisted"><option value="true" ${p.kill_blacklisted ? "selected" : ""}>是</option><option value="false" ${!p.kill_blacklisted ? "selected" : ""}>否</option></select>
      </label>
    </div>
    <div class="form-row">
      <label>白名单模式（仅允许列表内进程）
        <select name="process_whitelist_mode"><option value="false" ${!p.process_whitelist_mode ? "selected" : ""}>否</option><option value="true" ${p.process_whitelist_mode ? "selected" : ""}>是</option></select>
      </label>
      <label>允许远程关机/重启
        <select name="allow_shutdown"><option value="true" ${p.allow_shutdown !== false ? "selected" : ""}>是</option><option value="false" ${p.allow_shutdown === false ? "selected" : ""}>否</option></select>
      </label>
    </div>
    <label>进程黑名单（逗号分隔）
      <textarea name="process_blacklist" rows="3">${escAttr((p.process_blacklist || []).join(", "))}</textarea>
    </label>
    <label>进程白名单（白名单模式生效，逗号分隔）
      <textarea name="process_whitelist" rows="3">${escAttr((p.process_whitelist || []).join(", "))}</textarea>
    </label>
    <label>域名黑名单（逗号分隔，匹配反查主机名/地址）
      <textarea name="domain_blacklist" rows="3">${escAttr((p.domain_blacklist || []).join(", "))}</textarea>
    </label>
    <div class="muted">策略 ID：<span class="mono">${esc(p.id)}</span></div>
  `;
}

async function savePolicy() {
  if (!state.policy) return;
  const fd = new FormData($("#policyForm"));
  const split = (s) => String(s || "").split(/[,，\n]/).map((x) => x.trim()).filter(Boolean);
  const body = {
    ...state.policy,
    name: fd.get("name"),
    enabled: fd.get("enabled") === "true",
    collect_interval_sec: Number(fd.get("collect_interval_sec")),
    report_top_n_processes: Number(fd.get("report_top_n_processes")),
    max_cpu_percent: Number(fd.get("max_cpu_percent")),
    max_mem_percent: Number(fd.get("max_mem_percent")),
    max_disk_percent: Number(fd.get("max_disk_percent")),
    kill_blacklisted: fd.get("kill_blacklisted") === "true",
    process_whitelist_mode: fd.get("process_whitelist_mode") === "true",
    allow_shutdown: fd.get("allow_shutdown") === "true",
    process_blacklist: split(fd.get("process_blacklist")),
    process_whitelist: split(fd.get("process_whitelist")),
    domain_blacklist: split(fd.get("domain_blacklist")),
  };
  await api(`/api/policies/${encodeURIComponent(body.id)}`, { method: "PUT", body: JSON.stringify(body) });
  alert("策略已保存，学生机将在下次心跳拉取");
  await loadPolicies();
}

async function createPolicy() {
  const name = prompt("新策略名称", "考试模式");
  if (!name) return;
  const base = state.policy || state.policies[0] || {};
  const body = {
    ...base,
    id: "",
    name,
    process_whitelist_mode: !!base.process_whitelist_mode,
    process_whitelist: base.process_whitelist || [],
    allow_shutdown: base.allow_shutdown !== false,
  };
  const { policy } = await api("/api/policies", { method: "POST", body: JSON.stringify(body) });
  state.selectedPolicyId = policy.id;
  await loadPolicies();
}

async function deletePolicy() {
  if (!state.policy) return;
  if (state.policy.id === "default") {
    alert("默认策略不可删除");
    return;
  }
  if (!confirm(`删除策略「${state.policy.name}」？已分配该策略的学生机会回到默认策略。`)) return;
  await api(`/api/policies/${encodeURIComponent(state.policy.id)}`, { method: "DELETE" });
  state.selectedPolicyId = "default";
  await loadPolicies();
}

const COMMAND_TYPE_LABELS = {
  message: "消息",
  ping: "Ping",
  kill_process: "结束进程",
  refresh_policy: "刷新策略",
  shutdown: "关机",
  restart: "重启",
  update: "升级",
  upgrade: "升级",
};

const COMMAND_STATUS_LABELS = {
  pending: "待投递",
  delivered: "已投递",
  done: "完成",
  failed: "失败",
};

function commandTypeLabel(type) {
  const t = String(type || "");
  return COMMAND_TYPE_LABELS[t] || t || "-";
}

function commandStatusLabel(status) {
  const s = String(status || "");
  return COMMAND_STATUS_LABELS[s] || s || "-";
}

function commandPayloadText(c) {
  const payload = c?.payload;
  if (!payload || typeof payload !== "object") return "";
  return Object.values(payload)
    .map((v) => String(v ?? "").trim())
    .filter(Boolean)
    .join(" ");
}

function matchesCommandFilters(c) {
  const agent = String(state.commandFilterAgent || "").trim();
  if (agent && String(c?.agent_id || "") !== agent) return false;

  const type = String(state.commandFilterType || "").trim();
  if (type) {
    const ct = String(c?.type || "");
    const matched = type === "update" ? ct === "update" || ct === "upgrade" : ct === type;
    if (!matched) return false;
  }

  const status = String(state.commandFilterStatus || "").trim();
  if (status && String(c?.status || "") !== status) return false;

  const needle = String(state.commandFilterKeyword || "").trim().toLowerCase();
  if (!needle) return true;
  return matchesMonitorFilter(
    needle,
    c.agent_id,
    c.type,
    commandTypeLabel(c.type),
    c.status,
    commandStatusLabel(c.status),
    commandPayloadText(c),
    c.result,
    formatCommandResultPlain(c),
    fmtTime(c.created_at)
  );
}

function formatCommandResultPlain(c) {
  const raw = String(c?.result || "").trim();
  if (!raw) return "";
  if (raw.startsWith("reply:")) return raw.slice("reply:".length).trim() || raw;
  const map = {
    acked: "已确认（知道了）",
    dismissed: "已关闭（未回复）",
    timeout: "超时未确认",
    shown: "已通知（无法回复）",
    "等待学生确认…": "等待学生确认…",
    "message shown": "已展示",
  };
  return map[raw] || raw;
}

function renderCommandAgentFilter() {
  const sel = $("#commandFilterAgent");
  if (!sel) return;
  const selected = String(state.commandFilterAgent || "");
  const byId = new Map((state.agents || []).map((a) => [a.id, a]));
  for (const c of state.commands || []) {
    const id = String(c?.agent_id || "").trim();
    if (id && !byId.has(id)) byId.set(id, { id });
  }
  const agents = [...byId.values()].sort((a, b) =>
    monitorAgentLabel(a).localeCompare(monitorAgentLabel(b), "zh")
  );
  const keepFocus = document.activeElement === sel;
  sel.innerHTML =
    `<option value="">全部</option>` +
    agents.map((a) => `<option value="${escAttr(a.id)}">${esc(monitorAgentLabel(a))}</option>`).join("");
  const next = selected && byId.has(selected) ? selected : "";
  sel.value = next;
  if (state.commandFilterAgent !== next) {
    state.commandFilterAgent = next;
    localStorage.setItem("proctor_command_filter_agent", next);
  }
  if (keepFocus) sel.focus();
}

function syncCommandFilterControls() {
  const agentSel = $("#commandFilterAgent");
  if (agentSel && document.activeElement !== agentSel) {
    agentSel.value = state.commandFilterAgent || "";
  }
  const typeSel = $("#commandFilterType");
  if (typeSel && document.activeElement !== typeSel) {
    typeSel.value = state.commandFilterType || "";
  }
  const statusSel = $("#commandFilterStatus");
  if (statusSel && document.activeElement !== statusSel) {
    statusSel.value = state.commandFilterStatus || "";
  }
  const keyword = $("#commandFilterKeyword");
  if (keyword && document.activeElement !== keyword) {
    keyword.value = state.commandFilterKeyword || "";
  }
}

function renderCommandRows(commands) {
  const filtered = (commands || []).filter(matchesCommandFilters);
  return table(
    ["时间", "学生机", "类型", "状态", "结果"],
    filtered.map((c) => [
      fmtTime(c.created_at),
      `<span class="mono">${esc(c.agent_id)}</span>`,
      formatCommandType(c),
      formatCommandStatus(c.status),
      formatCommandResult(c),
    ])
  );
}

function applyCommandFilters() {
  $("#commandList").innerHTML = renderCommandRows(state.commands);
}

async function loadCommands() {
  const [commandsRes, retention, agentsRes] = await Promise.all([
    api("/api/commands"),
    api("/api/commands/retention"),
    api("/api/agents").catch(() => ({ agents: state.agents || [] })),
  ]);
  state.agents = agentsRes.agents || state.agents || [];
  state.commands = commandsRes.commands || [];
  const input = $("#commandRetentionDays");
  if (input && document.activeElement !== input) {
    input.value = retention.days ?? commandsRes.days ?? 7;
    if (retention.min != null) input.min = retention.min;
    if (retention.max != null) input.max = retention.max;
  }
  renderCommandAgentFilter();
  syncCommandFilterControls();
  applyCommandFilters();
}

function formatCommandType(c) {
  const type = String(c?.type || "");
  const label = commandTypeLabel(type);
  if (type === "message") {
    const text = String(c?.payload?.text || "").trim();
    if (text) {
      return `${esc(label)}<div class="muted" style="margin-top:4px;max-width:280px;white-space:pre-wrap;word-break:break-word">${esc(text)}</div>`;
    }
  }
  return esc(label);
}

function formatCommandStatus(status) {
  return esc(commandStatusLabel(status));
}

function formatCommandResult(c) {
  const raw = String(c?.result || "").trim();
  if (!raw) return "-";
  if (raw.startsWith("reply:")) {
    const reply = raw.slice("reply:".length).trim();
    return `<span class="badge warn">学生回复</span><div style="margin-top:4px;max-width:320px;white-space:pre-wrap;word-break:break-word">${esc(reply || "-")}</div>`;
  }
  const map = {
    acked: "已确认（知道了）",
    dismissed: "已关闭（未回复）",
    timeout: "超时未确认",
    shown: "已通知（无法回复）",
    "等待学生确认…": "等待学生确认…",
    "message shown": "已展示",
  };
  if (map[raw]) return esc(map[raw]);
  return esc(raw);
}

async function saveCommandRetention() {
  const input = $("#commandRetentionDays");
  const days = Number(input?.value);
  if (!Number.isFinite(days) || !Number.isInteger(days)) {
    throw new Error("请输入有效的整数天数");
  }
  const min = Number(input.min) || 1;
  const max = Number(input.max) || 365;
  if (days < min || days > max) {
    throw new Error(`每台机器保留天数需在 ${min}–${max} 之间`);
  }
  const res = await api("/api/commands/retention", {
    method: "PUT",
    body: JSON.stringify({ days }),
  });
  const deleted = Number(res.deleted) || 0;
  alert(
    deleted > 0
      ? `已保存，并按每台机器清理超期指令（共 ${deleted} 条）`
      : "已保存（已按每台机器清理超期指令）"
  );
  await loadCommands();
}

function table(headers, rows, ids = []) {
  if (!rows.length) return '<div class="empty">暂无数据</div>';
  return `<table><thead><tr>${headers.map((h) => `<th>${h}</th>`).join("")}</tr></thead>
  <tbody>${rows.map((r, i) => `<tr class="${ids[i] ? "clickable" : ""}" ${ids[i] ? `data-id="${escAttr(ids[i])}"` : ""}>${r.map((c) => `<td>${c}</td>`).join("")}</tr>`).join("")}</tbody></table>`;
}

function badgeOnline(on) {
  return on ? '<span class="badge ok">在线</span>' : '<span class="badge off">离线</span>';
}
function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}
function escAttr(s) { return esc(s); }
function num(v) { return (Number(v) || 0).toFixed(1); }
function fmtUptime(sec) {
  sec = Number(sec) || 0;
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  return `${d}天 ${h}时 ${m}分`;
}

$("#token").value = state.token;
const refreshSelect = $("#refreshInterval");
if (refreshSelect) {
  const allowed = ["0", "3", "5", "10", "15", "30"];
  const cur = String(Number.isFinite(state.refreshSec) ? state.refreshSec : 5);
  refreshSelect.value = allowed.includes(cur) ? cur : "5";
  state.refreshSec = Number(refreshSelect.value);
  refreshSelect.addEventListener("change", () => {
    state.refreshSec = Number(refreshSelect.value);
    localStorage.setItem("proctor_refresh_sec", String(state.refreshSec));
    setupAutoRefresh();
  });
}
$("#saveToken").addEventListener("click", () => {
  state.token = $("#token").value.trim();
  localStorage.setItem("proctor_token", state.token);
  refresh();
});
$("#refreshBtn").addEventListener("click", () => refresh());
$("#broadcastBtn")?.addEventListener("click", () => {
  broadcastTeacherMessage().catch((e) => alert(e.message));
});
$("#agentsBroadcastBtn")?.addEventListener("click", () => {
  broadcastTeacherMessage().catch((e) => alert(e.message));
});
$("#alertsRefreshBtn")?.addEventListener("click", () => {
  loadAlerts().catch((e) => alert("加载失败: " + e.message));
});
$("#commandsRefreshBtn")?.addEventListener("click", () => {
  loadCommands().catch((e) => alert("加载失败: " + e.message));
});
$("#updatesRefreshBtn")?.addEventListener("click", () => {
  loadUpdates().catch((e) => alert("加载失败: " + e.message));
});
$("#updateAgentBtn")?.addEventListener("click", async () => {
  const id = ($("#updateAgentSelect")?.value || "").trim();
  const ver = ($("#updateTargetVersion")?.value || "").trim();
  if (!id) {
    alert("请选择学生机");
    return;
  }
  if (!ver) {
    alert("请选择目标版本");
    return;
  }
  if (!confirm(`确认将所选学生机升级到 ${ver}？`)) return;
  try {
    await upgradeAgentToVersion(id, ver);
    alert(`已下发升级指令 → ${ver}`);
  } catch (e) {
    alert(e.message);
  }
});
$("#updateAllOnlineBtn")?.addEventListener("click", async () => {
  const ver = ($("#updateTargetVersion")?.value || "").trim();
  if (!ver) {
    alert("请选择目标版本");
    return;
  }
  const online = (state.agents || []).filter((a) => a.online);
  if (!online.length) {
    alert("当前没有在线学生机");
    return;
  }
  if (!confirm(`确认将全部 ${online.length} 台在线学生机升级到 ${ver}？`)) return;
  try {
    const data = await api("/api/agents/update", {
      method: "POST",
      body: JSON.stringify({
        agent_ids: online.map((a) => a.id),
        version: ver,
      }),
    });
    const queued = Number(data?.queued ?? 0);
    const total = Number(data?.total ?? online.length);
    alert(`已向 ${queued}/${total} 台在线学生机下发升级 → ${ver}`);
  } catch (e) {
    alert(e.message);
  }
});
$("#savePolicyBtn").addEventListener("click", (e) => { e.preventDefault(); savePolicy().catch((err) => alert(err.message)); });
$("#newPolicyBtn")?.addEventListener("click", () => createPolicy().catch((err) => alert(err.message)));
$("#deletePolicyBtn")?.addEventListener("click", () => deletePolicy().catch((err) => alert(err.message)));
$("#saveAlertRetentionBtn")?.addEventListener("click", () => {
  saveAlertRetention().catch((err) => alert(err.message));
});
$("#alertRetentionLimit")?.addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    saveAlertRetention().catch((err) => alert(err.message));
  }
});
$("#saveCommandRetentionBtn")?.addEventListener("click", () => {
  saveCommandRetention().catch((err) => alert(err.message));
});
$("#commandRetentionDays")?.addEventListener("keydown", (e) => {
  if (e.key === "Enter") {
    e.preventDefault();
    saveCommandRetention().catch((err) => alert(err.message));
  }
});
$("#alertFilter")?.addEventListener("input", (e) => {
  const t = e.target;
  if (!(t instanceof HTMLInputElement)) return;
  state.alertFilter = t.value;
  localStorage.setItem("proctor_alert_filter", state.alertFilter);
  $("#alertList").innerHTML = renderAlertRows(state.alerts);
  bindAlertAckButtons();
});
$("#commandFilterAgent")?.addEventListener("change", (e) => {
  const t = e.target;
  if (!(t instanceof HTMLSelectElement)) return;
  state.commandFilterAgent = t.value || "";
  localStorage.setItem("proctor_command_filter_agent", state.commandFilterAgent);
  applyCommandFilters();
});
$("#commandFilterType")?.addEventListener("change", (e) => {
  const t = e.target;
  if (!(t instanceof HTMLSelectElement)) return;
  state.commandFilterType = t.value || "";
  localStorage.setItem("proctor_command_filter_type", state.commandFilterType);
  applyCommandFilters();
});
$("#commandFilterStatus")?.addEventListener("change", (e) => {
  const t = e.target;
  if (!(t instanceof HTMLSelectElement)) return;
  state.commandFilterStatus = t.value || "";
  localStorage.setItem("proctor_command_filter_status", state.commandFilterStatus);
  applyCommandFilters();
});
$("#commandFilterKeyword")?.addEventListener("input", (e) => {
  const t = e.target;
  if (!(t instanceof HTMLInputElement)) return;
  state.commandFilterKeyword = t.value;
  localStorage.setItem("proctor_command_filter_keyword", state.commandFilterKeyword);
  applyCommandFilters();
});
$("#agentsAgentSelect")?.addEventListener("change", () => {
  const id = $("#agentsAgentSelect").value;
  setSelectedAgentId(id || null);
  if (id) {
    showAgent(id).catch((e) => alert(e.message));
  } else {
    $("#agentsList")?.querySelectorAll("tr.active-row").forEach((tr) => tr.classList.remove("active-row"));
    $("#agentDetail").innerHTML = '<div class="empty">选择一台学生机查看详情</div>';
  }
});
$("#monitorAgentSelect")?.addEventListener("change", () => {
  const id = $("#monitorAgentSelect").value;
  setSelectedAgentId(id || null);
  if (id) {
    showMonitor(id).catch((e) => alert(e.message));
  } else {
    $("#monitorDetail").innerHTML = '<div class="empty">选择一台学生机查看监控数据</div>';
  }
});
$("#filesAgentSelect")?.addEventListener("change", () => {
  const id = $("#filesAgentSelect").value;
  setSelectedAgentId(id || null);
  if (id) {
    state.fsAgentId = id;
    state.fsPath = "";
    $("#fsPreview")?.classList.add("hidden");
    fsBrowse("", { silent: false }).catch((e) => alert(e.message));
  } else {
    resetFilesEmpty();
  }
});
$("#termAgentSelect")?.addEventListener("change", () => {
  const id = $("#termAgentSelect").value;
  setSelectedAgentId(id || null);
  if ($("#sshHost")) {
    const agent = id ? state.agents.find((a) => a.id === id) : null;
    $("#sshHost").value = agent?.ip || "";
  }
  syncTermUI();
});
$("#termMode")?.addEventListener("change", syncTermModeFields);
$("#termConnectBtn")?.addEventListener("click", termConnect);
$("#termDisconnectBtn")?.addEventListener("click", () => {
  termDisconnect();
  state.term?.writeln("\r\n已断开。\r\n");
});
$("#fsUpBtn")?.addEventListener("click", () => {
  if (!state.fsPath) {
    fsBrowse("");
    return;
  }
  fsBrowse(parentPath(state.fsPath));
});
$("#fsRefreshBtn")?.addEventListener("click", () => fsBrowse(state.fsPath || ""));
$("#fsMkdirBtn")?.addEventListener("click", () => fsMkdir());
$("#fsUploadBtn")?.addEventListener("click", (e) => {
  if (state.fsReadOnly) {
    e.preventDefault();
    alert(FS_READONLY_TIP);
  }
});
$("#fsUploadInput")?.addEventListener("change", (e) => {
  const file = e.target.files?.[0];
  e.target.value = "";
  if (file) fsUpload(file);
});
$("#fsReadOnlyToggle")?.addEventListener("change", (e) => {
  setFSReadOnly(!!e.target.checked);
});
$$(".nav-item").forEach((b) => b.addEventListener("click", () => setView(b.dataset.view)));

updateClock();
setInterval(updateClock, 1000);
setupAutoRefresh();
applyFSReadOnlyUI();
setView(state.view);
