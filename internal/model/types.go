package model

import "time"

// AgentInfo is registered student machine metadata.
type AgentInfo struct {
	ID           string    `json:"id"`
	Hostname     string    `json:"hostname"`
	OS           string    `json:"os"`
	Arch         string    `json:"arch"`
	IP           string    `json:"ip"`
	Version      string    `json:"version"`
	StudentName  string    `json:"student_name,omitempty"`
	Classroom    string    `json:"classroom,omitempty"`
	LastSeen     time.Time `json:"last_seen"`
	Online       bool      `json:"online"`
	RegisteredAt time.Time `json:"registered_at"`
}

// HeartbeatPayload is sent periodically by agents.
type HeartbeatPayload struct {
	AgentID     string         `json:"agent_id"`
	Hostname    string         `json:"hostname"`
	OS          string         `json:"os"`
	Arch        string         `json:"arch"`
	IP          string         `json:"ip"`
	Version     string         `json:"version"`
	StudentName string         `json:"student_name,omitempty"`
	Classroom   string         `json:"classroom,omitempty"`
	Timestamp   time.Time      `json:"timestamp"`
	Resources   ResourceSnap   `json:"resources"`
	NetIO       NetIOSnap      `json:"net_io"`
	DiskIO      DiskIOSnap     `json:"disk_io"`
	Disks       []DiskSnap     `json:"disks"`
	Processes   []ProcessSnap  `json:"processes"`
	Networks    []NetworkSnap  `json:"networks"`
	Alerts      []Alert        `json:"alerts,omitempty"`
}

// ResourceSnap captures CPU/memory usage.
type ResourceSnap struct {
	CPUPercent    float64 `json:"cpu_percent"`
	CPUCount      int     `json:"cpu_count"`
	MemTotal      uint64  `json:"mem_total"`
	MemUsed       uint64  `json:"mem_used"`
	MemPercent    float64 `json:"mem_percent"`
	SwapTotal     uint64  `json:"swap_total"`
	SwapUsed      uint64  `json:"swap_used"`
	SwapPercent   float64 `json:"swap_percent"`
	Load1         float64 `json:"load1"`
	Load5         float64 `json:"load5"`
	Load15        float64 `json:"load15"`
	UptimeSeconds uint64  `json:"uptime_seconds"`
}

// NetIOSnap captures aggregate and per-NIC network throughput.
type NetIOSnap struct {
	BytesSent       uint64        `json:"bytes_sent"`
	BytesRecv       uint64        `json:"bytes_recv"`
	PacketsSent     uint64        `json:"packets_sent"`
	PacketsRecv     uint64        `json:"packets_recv"`
	SendBps         float64       `json:"send_bps"` // upload bytes/sec
	RecvBps         float64       `json:"recv_bps"` // download bytes/sec
	PacketsSentPps  float64       `json:"packets_sent_pps"`
	PacketsRecvPps  float64       `json:"packets_recv_pps"`
	ConnEstablished int           `json:"conn_established"`
	ConnListen      int           `json:"conn_listen"`
	ConnTotal       int           `json:"conn_total"`
	Interfaces      []NetIfaceSnap `json:"interfaces,omitempty"`
}

// NetIfaceSnap is per-interface throughput.
type NetIfaceSnap struct {
	Name        string  `json:"name"`
	BytesSent   uint64  `json:"bytes_sent"`
	BytesRecv   uint64  `json:"bytes_recv"`
	PacketsSent uint64  `json:"packets_sent"`
	PacketsRecv uint64  `json:"packets_recv"`
	SendBps     float64 `json:"send_bps"`
	RecvBps     float64 `json:"recv_bps"`
}

// DiskIOSnap captures disk read/write throughput.
type DiskIOSnap struct {
	ReadBytes  uint64  `json:"read_bytes"`
	WriteBytes uint64  `json:"write_bytes"`
	ReadBps    float64 `json:"read_bps"`
	WriteBps   float64 `json:"write_bps"`
	ReadCount  uint64  `json:"read_count"`
	WriteCount uint64  `json:"write_count"`
}

// DiskSnap captures a mounted volume.
type DiskSnap struct {
	MountPoint string  `json:"mount_point"`
	Device     string  `json:"device"`
	FSType     string  `json:"fs_type"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	Percent    float64 `json:"percent"`
}

// ProcessSnap is a simplified process view.
type ProcessSnap struct {
	PID         int32   `json:"pid"`
	Name        string  `json:"name"`
	Username    string  `json:"username"`
	CPUPercent  float64 `json:"cpu_percent"`
	MemPercent  float64 `json:"mem_percent"`
	MemRSS      uint64  `json:"mem_rss"`
	Status      string  `json:"status"`
	Cmdline     string  `json:"cmdline,omitempty"`
	CreateTime  int64   `json:"create_time"`
	Blacklisted bool    `json:"blacklisted,omitempty"`
}

// NetworkSnap captures an active connection.
type NetworkSnap struct {
	FD         uint32 `json:"fd"`
	Family     string `json:"family"`
	Type       string `json:"type"`
	LAddr      string `json:"laddr"`
	RAddr      string `json:"raddr"`
	RemoteHost string `json:"remote_host,omitempty"` // reverse-DNS / hostname when available
	Status     string `json:"status"`
	PID        int32  `json:"pid"`
	Process    string `json:"process,omitempty"`
}

// Alert is a policy or health event.
type Alert struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Level     string    `json:"level"` // info, warn, critical
	Category  string    `json:"category"`
	Message   string    `json:"message"`
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Acked     bool      `json:"acked"`
}

// Policy defines enforcement rules pushed to agents.
type Policy struct {
	ID                   string   `json:"id"`
	Name                 string   `json:"name"`
	Enabled              bool     `json:"enabled"`
	ProcessBlacklist     []string `json:"process_blacklist"`
	ProcessWhitelistMode bool     `json:"process_whitelist_mode"`
	ProcessWhitelist     []string `json:"process_whitelist"`
	DomainBlacklist      []string `json:"domain_blacklist"`
	KillBlacklisted      bool     `json:"kill_blacklisted"`
	MaxCPUPercent        float64  `json:"max_cpu_percent"`
	MaxMemPercent        float64  `json:"max_mem_percent"`
	MaxDiskPercent       float64  `json:"max_disk_percent"`
	BlockUSBStorage      bool     `json:"block_usb_storage"`
	AllowShutdown        bool     `json:"allow_shutdown"`
	CollectIntervalSec   int      `json:"collect_interval_sec"`
	ReportTopNProcesses  int      `json:"report_top_n_processes"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// Command is a remote action for an agent.
type Command struct {
	ID        string            `json:"id"`
	AgentID   string            `json:"agent_id"`
	Type      string            `json:"type"` // kill_process, refresh_policy, message, shutdown, restart, update
	Payload   map[string]string `json:"payload,omitempty"` // message: text, reply(optional, default true)
	Status    string            `json:"status"` // pending, done, failed
	Result    string            `json:"result,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

// CommandResult is agent feedback for a command.
type CommandResult struct {
	CommandID string `json:"command_id"`
	AgentID   string `json:"agent_id"`
	Status    string `json:"status"`
	Result    string `json:"result"`
}

// MaxFSFileBytes limits remote read/write payload size (raw bytes before base64).
const MaxFSFileBytes = 4 << 20 // 4 MiB

// FSJob is a remote filesystem operation delivered via heartbeat.
type FSJob struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Op        string    `json:"op"` // roots, list, stat, read, write, mkdir, delete, rename
	Path      string    `json:"path,omitempty"`
	Dest      string    `json:"dest,omitempty"`    // rename target
	Content   string    `json:"content,omitempty"` // base64 for write
	Status    string    `json:"status"`           // pending, delivered, done, failed
	Result    *FSResult `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FSResult is agent feedback for an FS job.
type FSResult struct {
	Op        string    `json:"op"`
	Path      string    `json:"path,omitempty"`
	Entries   []FSEntry `json:"entries,omitempty"`
	Content   string    `json:"content,omitempty"` // base64
	Name      string    `json:"name,omitempty"`
	IsDir     bool      `json:"is_dir,omitempty"`
	Size      int64     `json:"size,omitempty"`
	Mode      string    `json:"mode,omitempty"`
	ModTime   time.Time `json:"mod_time,omitempty"`
	Truncated bool      `json:"truncated,omitempty"`
	Message   string    `json:"message,omitempty"`
}

// FSEntry is one directory listing item.
type FSEntry struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	IsDir   bool      `json:"is_dir"`
	Size    int64     `json:"size"`
	Mode    string    `json:"mode,omitempty"`
	ModTime time.Time `json:"mod_time"`
}

// FSJobResult is posted by the agent after executing an FS job.
type FSJobResult struct {
	JobID   string    `json:"job_id"`
	AgentID string    `json:"agent_id"`
	Status  string    `json:"status"` // done | failed
	Result  *FSResult `json:"result,omitempty"`
	Error   string    `json:"error,omitempty"`
}

// DashboardStats aggregates console overview.
type DashboardStats struct {
	TotalAgents   int     `json:"total_agents"`
	OnlineAgents  int     `json:"online_agents"`
	OfflineAgents int     `json:"offline_agents"`
	AlertCount    int     `json:"alert_count"`
	AvgCPU        float64 `json:"avg_cpu"`
	AvgMem        float64 `json:"avg_mem"`
}

// ShellOffer is delivered to an agent via heartbeat when a teacher opens an Agent terminal.
type ShellOffer struct {
	SessionID string `json:"session_id"`
	Cols      int    `json:"cols,omitempty"`
	Rows      int    `json:"rows,omitempty"`
}

// ShellWSMessage is the JSON protocol for teacher/agent terminal websockets.
type ShellWSMessage struct {
	Type    string `json:"type"` // ready, input, output, resize, error, ping, pong
	Data    string `json:"data,omitempty"`
	Cols    int    `json:"cols,omitempty"`
	Rows    int    `json:"rows,omitempty"`
	Message string `json:"message,omitempty"`
}
