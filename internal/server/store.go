package server

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/billcoding/proctor/internal/model"
	_ "modernc.org/sqlite"
)

const (
	defaultAlertRetention = 200
	minAlertRetention     = 10
	maxAlertRetention     = 10000
	settingAlertRetention = "alert_retention_limit"

	defaultCommandRetentionDays = 7
	minCommandRetentionDays     = 1
	maxCommandRetentionDays     = 365
	settingCommandRetention     = "command_retention_days"
)

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func OpenStore(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureDefaultPolicy(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureAlertRetentionSetting(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.ensureCommandRetentionSetting(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS agents (
  id TEXT PRIMARY KEY,
  hostname TEXT,
  os TEXT,
  arch TEXT,
  ip TEXT,
  version TEXT,
  student_name TEXT,
  classroom TEXT,
  last_seen TEXT,
  registered_at TEXT,
  last_heartbeat TEXT,
  meta_locked INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS alerts (
  id TEXT PRIMARY KEY,
  agent_id TEXT,
  level TEXT,
  category TEXT,
  message TEXT,
  detail TEXT,
  created_at TEXT,
  acked INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS policies (
  id TEXT PRIMARY KEY,
  name TEXT,
  body TEXT,
  updated_at TEXT
);
CREATE TABLE IF NOT EXISTS agent_policy (
  agent_id TEXT PRIMARY KEY,
  policy_id TEXT
);
CREATE TABLE IF NOT EXISTS commands (
  id TEXT PRIMARY KEY,
  agent_id TEXT,
  type TEXT,
  payload TEXT,
  status TEXT,
  result TEXT,
  created_at TEXT,
  updated_at TEXT
);
CREATE TABLE IF NOT EXISTS fs_jobs (
  id TEXT PRIMARY KEY,
  agent_id TEXT,
  op TEXT,
  path TEXT,
  dest TEXT,
  content TEXT,
  status TEXT,
  result TEXT,
  error TEXT,
  created_at TEXT,
  updated_at TEXT
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT
);
CREATE INDEX IF NOT EXISTS idx_alerts_created ON alerts(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_alerts_dedup ON alerts(agent_id, category, message, acked, created_at);
CREATE INDEX IF NOT EXISTS idx_commands_agent_status ON commands(agent_id, status);
CREATE INDEX IF NOT EXISTS idx_commands_agent_created ON commands(agent_id, created_at);
CREATE INDEX IF NOT EXISTS idx_fs_jobs_agent_status ON fs_jobs(agent_id, status);
`)
	if err != nil {
		return err
	}
	// Best-effort column add for upgrades from older schemas.
	_, _ = s.db.Exec(`ALTER TABLE agents ADD COLUMN meta_locked INTEGER DEFAULT 0`)
	_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS fs_jobs (
  id TEXT PRIMARY KEY,
  agent_id TEXT,
  op TEXT,
  path TEXT,
  dest TEXT,
  content TEXT,
  status TEXT,
  result TEXT,
  error TEXT,
  created_at TEXT,
  updated_at TEXT
)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_fs_jobs_agent_status ON fs_jobs(agent_id, status)`)
	_, _ = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_commands_agent_created ON commands(agent_id, created_at)`)
	_, _ = s.db.Exec(`CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT
)`)
	return nil
}

func (s *Store) ensureAlertRetentionSetting() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM settings WHERE key=?`, settingAlertRetention).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, settingAlertRetention, strconv.Itoa(defaultAlertRetention))
		return err
	}
	return nil
}

func (s *Store) ensureCommandRetentionSetting() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM settings WHERE key=?`, settingCommandRetention).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)`, settingCommandRetention, strconv.Itoa(defaultCommandRetentionDays))
		return err
	}
	return nil
}

func (s *Store) GetCommandRetentionDays() (int, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, settingCommandRetention).Scan(&v)
	if err == sql.ErrNoRows {
		return defaultCommandRetentionDays, nil
	}
	if err != nil {
		return defaultCommandRetentionDays, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return defaultCommandRetentionDays, nil
	}
	if n < minCommandRetentionDays {
		return minCommandRetentionDays, nil
	}
	if n > maxCommandRetentionDays {
		return maxCommandRetentionDays, nil
	}
	return n, nil
}

func (s *Store) SetCommandRetentionDays(days int) (int64, error) {
	if days < minCommandRetentionDays || days > maxCommandRetentionDays {
		return 0, fmt.Errorf("days must be between %d and %d", minCommandRetentionDays, maxCommandRetentionDays)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingCommandRetention, strconv.Itoa(days))
	if err != nil {
		return 0, err
	}
	return s.pruneCommandsLocked(days)
}

// pruneCommandsLocked deletes commands older than `days` for every agent_id
// (same day window applied per machine via created_at).
func (s *Store) pruneCommandsLocked(days int) (int64, error) {
	if days <= 0 {
		days = defaultCommandRetentionDays
	}
	cutoff := time.Now().UTC().Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM commands WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func (s *Store) GetAlertRetentionLimit() (int, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, settingAlertRetention).Scan(&v)
	if err == sql.ErrNoRows {
		return defaultAlertRetention, nil
	}
	if err != nil {
		return defaultAlertRetention, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return defaultAlertRetention, nil
	}
	if n < minAlertRetention {
		return minAlertRetention, nil
	}
	if n > maxAlertRetention {
		return maxAlertRetention, nil
	}
	return n, nil
}

func (s *Store) SetAlertRetentionLimit(limit int) (int64, error) {
	if limit < minAlertRetention || limit > maxAlertRetention {
		return 0, fmt.Errorf("limit must be between %d and %d", minAlertRetention, maxAlertRetention)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
ON CONFLICT(key) DO UPDATE SET value=excluded.value`, settingAlertRetention, strconv.Itoa(limit))
	if err != nil {
		return 0, err
	}
	return s.pruneAlertsLocked(limit)
}

// pruneAlertsLocked keeps the newest `limit` alerts per agent_id and deletes older ones.
func (s *Store) pruneAlertsLocked(limit int) (int64, error) {
	if limit <= 0 {
		limit = defaultAlertRetention
	}
	res, err := s.db.Exec(`DELETE FROM alerts WHERE id IN (
SELECT id FROM (
  SELECT id, ROW_NUMBER() OVER (
    PARTITION BY agent_id ORDER BY created_at DESC, id DESC
  ) AS rn
  FROM alerts
) WHERE rn > ?
)`, limit)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// alertListLimit returns a display cap for the aggregated alerts list:
// per-agent retention × agent count, bounded for API responsiveness.
func (s *Store) alertListLimit(perAgent int) int {
	if perAgent <= 0 {
		perAgent = defaultAlertRetention
	}
	var agents int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM agents`).Scan(&agents)
	if agents < 1 {
		agents = 1
	}
	listLimit := perAgent * agents
	if listLimit > maxAlertRetention {
		return maxAlertRetention
	}
	return listLimit
}

func (s *Store) ensureDefaultPolicy() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM policies`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		p := model.Policy{
			ID: "default", Name: "默认课堂策略", Enabled: true,
			KillBlacklisted: true, AllowShutdown: true,
			KillScanIntervalSec:  model.DefaultKillScanIntervalSec,
			KillScheduleMode:     model.KillScheduleAllDay,
			KillActions:          []string{model.KillActionKill, model.KillActionAlert},
			KillWarnCountdownSec: model.DefaultKillWarnCountdownSec,
			KillCooldownSec:      model.DefaultKillCooldownSec,
			MaxCPUPercent: 95, MaxMemPercent: 95, MaxDiskPercent: 92,
			CollectIntervalSec: 5, ReportTopNProcesses: 30,
			ProcessBlacklist: []string{"steam", "epicgameslauncher", "discord", "qqmusic", "bilibili", "douyin", "minecraft", "wegame"},
			DomainBlacklist:  []string{"tiktok.com", "douyin.com", "bilibili.com", "steampowered.com", "wegame.qq.com", "wegame.com"},
			UpdatedAt:        time.Now().UTC(),
		}
		return s.SavePolicy(p)
	}
	if p, err := s.GetPolicy("default"); err == nil {
		changed := false
		// Bump legacy default collect interval 15s -> 5s for fresher monitoring.
		if p.CollectIntervalSec == 15 {
			p.CollectIntervalSec = 5
			changed = true
		}
		// Merge newly banned defaults (e.g. WeGame) into existing default policy.
		if appendMissingFold(&p.ProcessBlacklist, "wegame") {
			changed = true
		}
		if appendMissingFold(&p.DomainBlacklist, "wegame.qq.com", "wegame.com") {
			changed = true
		}
		if changed {
			_ = s.SavePolicy(p)
		}
	}
	return nil
}

// appendMissingFold appends items not already present (case-insensitive).
func appendMissingFold(dst *[]string, items ...string) bool {
	if dst == nil {
		return false
	}
	have := make(map[string]struct{}, len(*dst))
	for _, v := range *dst {
		have[strings.ToLower(strings.TrimSpace(v))] = struct{}{}
	}
	changed := false
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := have[key]; ok {
			continue
		}
		*dst = append(*dst, item)
		have[key] = struct{}{}
		changed = true
	}
	return changed
}

func (s *Store) UpsertHeartbeat(hb model.HeartbeatPayload) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Format(time.RFC3339)
	body, _ := json.Marshal(hb)
	var exists int
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM agents WHERE id=?`, hb.AgentID).Scan(&exists)
	if exists == 0 {
		_, err := s.db.Exec(`INSERT INTO agents(id,hostname,os,arch,ip,version,student_name,classroom,last_seen,registered_at,last_heartbeat,meta_locked)
VALUES(?,?,?,?,?,?,?,?,?,?,?,0)`,
			hb.AgentID, hb.Hostname, hb.OS, hb.Arch, hb.IP, hb.Version, hb.StudentName, hb.Classroom, now, now, string(body))
		return err
	}
	// Preserve teacher-edited student_name/classroom when meta_locked=1.
	_, err := s.db.Exec(`UPDATE agents SET hostname=?,os=?,arch=?,ip=?,version=?,
student_name=CASE WHEN COALESCE(meta_locked,0)=1 THEN student_name ELSE ? END,
classroom=CASE WHEN COALESCE(meta_locked,0)=1 THEN classroom ELSE ? END,
last_seen=?,last_heartbeat=? WHERE id=?`,
		hb.Hostname, hb.OS, hb.Arch, hb.IP, hb.Version, hb.StudentName, hb.Classroom, now, string(body), hb.AgentID)
	return err
}

func (s *Store) ListAgents(onlineAfter time.Duration) ([]model.AgentInfo, error) {
	rows, err := s.db.Query(`SELECT id,hostname,os,arch,ip,version,student_name,classroom,last_seen,registered_at FROM agents ORDER BY last_seen DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.AgentInfo
	cutoff := time.Now().Add(-onlineAfter)
	for rows.Next() {
		var a model.AgentInfo
		var lastSeen, regAt string
		if err := rows.Scan(&a.ID, &a.Hostname, &a.OS, &a.Arch, &a.IP, &a.Version, &a.StudentName, &a.Classroom, &lastSeen, &regAt); err != nil {
			return nil, err
		}
		a.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
		a.RegisteredAt, _ = time.Parse(time.RFC3339, regAt)
		a.Online = a.LastSeen.After(cutoff)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) GetAgent(id string, onlineAfter time.Duration) (*model.AgentInfo, *model.HeartbeatPayload, error) {
	var a model.AgentInfo
	var lastSeen, regAt, hbRaw string
	err := s.db.QueryRow(`SELECT id,hostname,os,arch,ip,version,student_name,classroom,last_seen,registered_at,last_heartbeat FROM agents WHERE id=?`, id).
		Scan(&a.ID, &a.Hostname, &a.OS, &a.Arch, &a.IP, &a.Version, &a.StudentName, &a.Classroom, &lastSeen, &regAt, &hbRaw)
	if err != nil {
		return nil, nil, err
	}
	a.LastSeen, _ = time.Parse(time.RFC3339, lastSeen)
	a.RegisteredAt, _ = time.Parse(time.RFC3339, regAt)
	a.Online = a.LastSeen.After(time.Now().Add(-onlineAfter))
	var hb model.HeartbeatPayload
	_ = json.Unmarshal([]byte(hbRaw), &hb)
	return &a, &hb, nil
}

func (s *Store) DeleteAgent(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM alerts WHERE agent_id=?`,
		`DELETE FROM commands WHERE agent_id=?`,
		`DELETE FROM fs_jobs WHERE agent_id=?`,
		`DELETE FROM agent_policy WHERE agent_id=?`,
		`DELETE FROM agents WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AddAlerts(alerts []model.Alert) error {
	if len(alerts) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT INTO alerts(id,agent_id,level,category,message,detail,created_at,acked) VALUES(?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	cutoff := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	for _, a := range alerts {
		var n int
		_ = tx.QueryRow(`SELECT COUNT(1) FROM alerts WHERE agent_id=? AND category=? AND message=? AND acked=0 AND created_at>=?`,
			a.AgentID, a.Category, a.Message, cutoff).Scan(&n)
		if n > 0 {
			continue
		}
		acked := 0
		if a.Acked {
			acked = 1
		}
		if _, err := stmt.Exec(a.ID, a.AgentID, a.Level, a.Category, a.Message, a.Detail, a.CreatedAt.UTC().Format(time.RFC3339), acked); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if limit, err := s.GetAlertRetentionLimit(); err == nil {
		_, _ = s.pruneAlertsLocked(limit)
	}
	return nil
}

func (s *Store) ListAlerts(limit int) ([]model.Alert, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id,agent_id,level,category,message,detail,created_at,acked FROM alerts ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Alert
	for rows.Next() {
		var a model.Alert
		var created string
		var acked int
		if err := rows.Scan(&a.ID, &a.AgentID, &a.Level, &a.Category, &a.Message, &a.Detail, &created, &acked); err != nil {
			return nil, err
		}
		a.CreatedAt, _ = time.Parse(time.RFC3339, created)
		a.Acked = acked == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) AckAlert(id string) error {
	_, err := s.db.Exec(`UPDATE alerts SET acked=1 WHERE id=?`, id)
	return err
}

func (s *Store) SavePolicy(p model.Policy) error {
	model.NormalizeKillPolicy(&p)
	p.UpdatedAt = time.Now().UTC()
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO policies(id,name,body,updated_at) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, body=excluded.body, updated_at=excluded.updated_at`,
		p.ID, p.Name, string(body), p.UpdatedAt.Format(time.RFC3339))
	return err
}

func (s *Store) GetPolicy(id string) (model.Policy, error) {
	if id == "" {
		id = "default"
	}
	var body string
	err := s.db.QueryRow(`SELECT body FROM policies WHERE id=?`, id).Scan(&body)
	if err != nil {
		return model.Policy{}, err
	}
	var p model.Policy
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return model.Policy{}, err
	}
	// Older policies omit allow_shutdown; treat missing as true so remote power works.
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(body), &raw) == nil {
		if _, ok := raw["allow_shutdown"]; !ok {
			p.AllowShutdown = true
		}
	}
	model.NormalizeKillPolicy(&p)
	return p, nil
}

func (s *Store) ListPolicies() ([]model.Policy, error) {
	rows, err := s.db.Query(`SELECT body FROM policies ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Policy
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var p model.Policy
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			return nil, err
		}
		model.NormalizeKillPolicy(&p)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePolicy(id string) error {
	if id == "" || id == "default" {
		return fmt.Errorf("cannot delete default policy")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Reassign agents on this policy back to default.
	if _, err := tx.Exec(`UPDATE agent_policy SET policy_id='default' WHERE policy_id=?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM policies WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("policy not found")
	}
	return tx.Commit()
}

func (s *Store) AssignPolicy(agentID, policyID string) error {
	if _, err := s.GetPolicy(policyID); err != nil {
		return fmt.Errorf("policy not found")
	}
	_, err := s.db.Exec(`INSERT INTO agent_policy(agent_id,policy_id) VALUES(?,?)
ON CONFLICT(agent_id) DO UPDATE SET policy_id=excluded.policy_id`, agentID, policyID)
	return err
}

func (s *Store) PolicyForAgent(agentID string) (model.Policy, error) {
	var policyID string
	err := s.db.QueryRow(`SELECT policy_id FROM agent_policy WHERE agent_id=?`, agentID).Scan(&policyID)
	if err == sql.ErrNoRows {
		return s.GetPolicy("default")
	}
	if err != nil {
		return model.Policy{}, err
	}
	return s.GetPolicy(policyID)
}

func (s *Store) AgentPolicyID(agentID string) string {
	var policyID string
	err := s.db.QueryRow(`SELECT policy_id FROM agent_policy WHERE agent_id=?`, agentID).Scan(&policyID)
	if err != nil || policyID == "" {
		return "default"
	}
	return policyID
}

func (s *Store) EnqueueCommand(cmd model.Command) error {
	payload, _ := json.Marshal(cmd.Payload)
	now := time.Now().UTC().Format(time.RFC3339)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO commands(id,agent_id,type,payload,status,result,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		cmd.ID, cmd.AgentID, cmd.Type, string(payload), cmd.Status, cmd.Result, now, now)
	if err != nil {
		return err
	}
	if days, err := s.GetCommandRetentionDays(); err == nil {
		_, _ = s.pruneCommandsLocked(days)
	}
	return nil
}

func (s *Store) PendingCommands(agentID string) ([]model.Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Deliver new pending commands, and redeliver stuck "delivered" ones after 60s.
	retryBefore := time.Now().UTC().Add(-60 * time.Second).Format(time.RFC3339)
	rows, err := s.db.Query(`SELECT id,agent_id,type,payload,status,result,created_at,updated_at FROM commands
WHERE agent_id=? AND (
  status='pending' OR (status='delivered' AND updated_at<=?)
) ORDER BY created_at`, agentID, retryBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Command
	ids := make([]string, 0)
	now := time.Now().UTC().Format(time.RFC3339)
	for rows.Next() {
		var c model.Command
		var payload, created, updated string
		if err := rows.Scan(&c.ID, &c.AgentID, &c.Type, &payload, &c.Status, &c.Result, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &c.Payload)
		c.CreatedAt, _ = time.Parse(time.RFC3339, created)
		c.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		_, _ = s.db.Exec(`UPDATE commands SET status='delivered', updated_at=? WHERE id=?`, now, id)
	}
	return out, nil
}

func (s *Store) CompleteCommand(res model.CommandResult) error {
	status := res.Status
	if status != "done" && status != "failed" {
		status = "done"
	}
	_, err := s.db.Exec(`UPDATE commands SET status=?, result=?, updated_at=? WHERE id=? AND agent_id=?`,
		status, res.Result, time.Now().UTC().Format(time.RFC3339), res.CommandID, res.AgentID)
	return err
}

func (s *Store) ListCommands(limit int) ([]model.Command, error) {
	if limit <= 0 {
		limit = 50
	}
	s.mu.Lock()
	if days, err := s.GetCommandRetentionDays(); err == nil {
		_, _ = s.pruneCommandsLocked(days)
	}
	s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id,agent_id,type,payload,status,result,created_at,updated_at FROM commands ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Command
	for rows.Next() {
		var c model.Command
		var payload, created, updated string
		if err := rows.Scan(&c.ID, &c.AgentID, &c.Type, &payload, &c.Status, &c.Result, &created, &updated); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(payload), &c.Payload)
		c.CreatedAt, _ = time.Parse(time.RFC3339, created)
		c.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Stats(onlineAfter time.Duration) (model.DashboardStats, error) {
	agents, err := s.ListAgents(onlineAfter)
	if err != nil {
		return model.DashboardStats{}, err
	}
	st := model.DashboardStats{TotalAgents: len(agents)}
	var cpuSum, memSum float64
	var onlineN int
	for _, a := range agents {
		if a.Online {
			st.OnlineAgents++
			_, hb, err := s.GetAgent(a.ID, onlineAfter)
			if err == nil && hb != nil {
				cpuSum += hb.Resources.CPUPercent
				memSum += hb.Resources.MemPercent
				onlineN++
			}
		} else {
			st.OfflineAgents++
		}
	}
	if onlineN > 0 {
		st.AvgCPU = cpuSum / float64(onlineN)
		st.AvgMem = memSum / float64(onlineN)
	}
	_ = s.db.QueryRow(`SELECT COUNT(1) FROM alerts WHERE acked=0`).Scan(&st.AlertCount)
	return st, nil
}

func (s *Store) UpdateAgentMeta(id, student, classroom string) error {
	res, err := s.db.Exec(`UPDATE agents SET student_name=?, classroom=?, meta_locked=1 WHERE id=?`, student, classroom, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("agent not found")
	}
	return nil
}

func (s *Store) EnqueueFSJob(job model.FSJob) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(`INSERT INTO fs_jobs(id,agent_id,op,path,dest,content,status,result,error,created_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		job.ID, job.AgentID, job.Op, job.Path, job.Dest, job.Content, job.Status, "", "", now, now)
	return err
}

func (s *Store) PendingFSJobs(agentID string) ([]model.FSJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	retryBefore := time.Now().UTC().Add(-60 * time.Second).Format(time.RFC3339)
	rows, err := s.db.Query(`SELECT id,agent_id,op,path,dest,content,status,result,error,created_at,updated_at FROM fs_jobs
WHERE agent_id=? AND (status='pending' OR (status='delivered' AND updated_at<=?)) ORDER BY created_at`, agentID, retryBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FSJob
	ids := make([]string, 0)
	now := time.Now().UTC().Format(time.RFC3339)
	for rows.Next() {
		job, err := scanFSJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
		ids = append(ids, job.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, id := range ids {
		_, _ = s.db.Exec(`UPDATE fs_jobs SET status='delivered', updated_at=? WHERE id=?`, now, id)
	}
	return out, nil
}

func (s *Store) CompleteFSJob(res model.FSJobResult) error {
	status := res.Status
	if status != "done" && status != "failed" {
		status = "done"
	}
	var resultJSON string
	if res.Result != nil {
		b, _ := json.Marshal(res.Result)
		resultJSON = string(b)
	}
	_, err := s.db.Exec(`UPDATE fs_jobs SET status=?, result=?, error=?, updated_at=? WHERE id=? AND agent_id=?`,
		status, resultJSON, res.Error, time.Now().UTC().Format(time.RFC3339), res.JobID, res.AgentID)
	return err
}

func (s *Store) GetFSJob(id string) (model.FSJob, error) {
	row := s.db.QueryRow(`SELECT id,agent_id,op,path,dest,content,status,result,error,created_at,updated_at FROM fs_jobs WHERE id=?`, id)
	return scanFSJob(row)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFSJob(row rowScanner) (model.FSJob, error) {
	var job model.FSJob
	var result, created, updated string
	if err := row.Scan(&job.ID, &job.AgentID, &job.Op, &job.Path, &job.Dest, &job.Content, &job.Status, &result, &job.Error, &created, &updated); err != nil {
		return model.FSJob{}, err
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339, created)
	job.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
	if result != "" {
		var fr model.FSResult
		if json.Unmarshal([]byte(result), &fr) == nil {
			job.Result = &fr
		}
	}
	return job, nil
}
