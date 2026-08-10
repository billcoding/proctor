package agent

import (
	"fmt"
	"net"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/billcoding/proctor/internal/model"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
)

type Collector struct {
	TopN int

	dnsMu    sync.Mutex
	dnsCache map[string]dnsCacheEntry

	ioMu       sync.Mutex
	prevNetAt  time.Time
	prevNet    map[string]gnet.IOCountersStat
	prevDiskAt time.Time
	prevDisk   disk.IOCountersStat
}

type dnsCacheEntry struct {
	host string
	at   time.Time
}

func NewCollector(topN int) *Collector {
	if topN <= 0 {
		topN = 30
	}
	return &Collector{
		TopN:     topN,
		dnsCache: map[string]dnsCacheEntry{},
		prevNet:  map[string]gnet.IOCountersStat{},
	}
}

func (c *Collector) Collect(agentID, student, classroom, version string, blacklist []string) (model.HeartbeatPayload, error) {
	hostInfo, _ := host.Info()
	hostname := ""
	if hostInfo != nil {
		hostname = hostInfo.Hostname
	}

	res, err := c.resources(hostInfo)
	if err != nil {
		return model.HeartbeatPayload{}, err
	}

	nets := c.networks()
	est, listen, total := countConns(nets)
	// On first collect, seed counters and wait briefly so rates are available immediately.
	if c.seedIO() {
		time.Sleep(350 * time.Millisecond)
	}

	return model.HeartbeatPayload{
		AgentID:     agentID,
		Hostname:    hostname,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		IP:          primaryIP(),
		Version:     version,
		StudentName: student,
		Classroom:   classroom,
		Timestamp:   time.Now().UTC(),
		Resources:   res,
		NetIO:       c.netIO(est, listen, total),
		DiskIO:      c.diskIO(),
		Disks:       c.disks(),
		Processes:   c.processes(blacklist),
		Networks:    nets,
	}, nil
}

func (c *Collector) seedIO() bool {
	c.ioMu.Lock()
	need := c.prevNetAt.IsZero() || c.prevDiskAt.IsZero()
	c.ioMu.Unlock()
	if !need {
		return false
	}
	_ = c.netIO(0, 0, 0)
	_ = c.diskIO()
	return true
}

func (c *Collector) resources(hi *host.InfoStat) (model.ResourceSnap, error) {
	var snap model.ResourceSnap
	if n, err := cpu.Counts(true); err == nil {
		snap.CPUCount = n
	}
	cpuPercents, err := cpu.Percent(300*time.Millisecond, false)
	if err == nil && len(cpuPercents) > 0 {
		snap.CPUPercent = cpuPercents[0]
	}
	vm, err := mem.VirtualMemory()
	if err != nil {
		return snap, fmt.Errorf("mem: %w", err)
	}
	snap.MemTotal = vm.Total
	snap.MemUsed = vm.Used
	snap.MemPercent = vm.UsedPercent

	if sm, err := mem.SwapMemory(); err == nil {
		snap.SwapTotal = sm.Total
		snap.SwapUsed = sm.Used
		snap.SwapPercent = sm.UsedPercent
	}
	if avg, err := load.Avg(); err == nil && avg != nil {
		snap.Load1, snap.Load5, snap.Load15 = avg.Load1, avg.Load5, avg.Load15
	}
	if hi != nil {
		snap.UptimeSeconds = hi.Uptime
	}
	return snap, nil
}

func (c *Collector) netIO(est, listen, total int) model.NetIOSnap {
	snap := model.NetIOSnap{
		ConnEstablished: est,
		ConnListen:      listen,
		ConnTotal:       total,
	}
	counters, err := gnet.IOCounters(true)
	if err != nil || len(counters) == 0 {
		return snap
	}

	now := time.Now()
	cur := make(map[string]gnet.IOCountersStat, len(counters))
	var sent, recv, ps, pr uint64
	ifaces := make([]model.NetIfaceSnap, 0, len(counters))

	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	elapsed := now.Sub(c.prevNetAt).Seconds()

	for _, st := range counters {
		if isLoopbackIface(st.Name) {
			continue
		}
		cur[st.Name] = st
		sent += st.BytesSent
		recv += st.BytesRecv
		ps += st.PacketsSent
		pr += st.PacketsRecv

		iface := model.NetIfaceSnap{
			Name:        st.Name,
			BytesSent:   st.BytesSent,
			BytesRecv:   st.BytesRecv,
			PacketsSent: st.PacketsSent,
			PacketsRecv: st.PacketsRecv,
		}
		if elapsed > 0.2 {
			if prev, ok := c.prevNet[st.Name]; ok {
				iface.SendBps = rate(st.BytesSent, prev.BytesSent, elapsed)
				iface.RecvBps = rate(st.BytesRecv, prev.BytesRecv, elapsed)
			}
		}
		ifaces = append(ifaces, iface)
	}
	sort.Slice(ifaces, func(i, j int) bool {
		return ifaces[i].RecvBps+ifaces[i].SendBps > ifaces[j].RecvBps+ifaces[j].SendBps
	})

	snap.BytesSent = sent
	snap.BytesRecv = recv
	snap.PacketsSent = ps
	snap.PacketsRecv = pr
	snap.Interfaces = ifaces

	if elapsed > 0.2 {
		var prevSent, prevRecv, prevPS, prevPR uint64
		for _, st := range c.prevNet {
			prevSent += st.BytesSent
			prevRecv += st.BytesRecv
			prevPS += st.PacketsSent
			prevPR += st.PacketsRecv
		}
		if len(c.prevNet) > 0 {
			snap.SendBps = rate(sent, prevSent, elapsed)
			snap.RecvBps = rate(recv, prevRecv, elapsed)
			snap.PacketsSentPps = rate(ps, prevPS, elapsed)
			snap.PacketsRecvPps = rate(pr, prevPR, elapsed)
		}
	}

	c.prevNet = cur
	c.prevNetAt = now
	return snap
}

func (c *Collector) diskIO() model.DiskIOSnap {
	var snap model.DiskIOSnap
	m, err := disk.IOCounters()
	if err != nil || len(m) == 0 {
		return snap
	}
	var cur disk.IOCountersStat
	for _, st := range m {
		cur.ReadBytes += st.ReadBytes
		cur.WriteBytes += st.WriteBytes
		cur.ReadCount += st.ReadCount
		cur.WriteCount += st.WriteCount
	}
	snap.ReadBytes = cur.ReadBytes
	snap.WriteBytes = cur.WriteBytes
	snap.ReadCount = cur.ReadCount
	snap.WriteCount = cur.WriteCount

	now := time.Now()
	c.ioMu.Lock()
	defer c.ioMu.Unlock()
	elapsed := now.Sub(c.prevDiskAt).Seconds()
	if elapsed > 0.2 && !c.prevDiskAt.IsZero() {
		snap.ReadBps = rate(cur.ReadBytes, c.prevDisk.ReadBytes, elapsed)
		snap.WriteBps = rate(cur.WriteBytes, c.prevDisk.WriteBytes, elapsed)
	}
	c.prevDisk = cur
	c.prevDiskAt = now
	return snap
}

func rate(cur, prev uint64, elapsed float64) float64 {
	if elapsed <= 0 || cur < prev {
		return 0
	}
	return float64(cur-prev) / elapsed
}

func isLoopbackIface(name string) bool {
	n := strings.ToLower(name)
	return n == "lo" || n == "lo0" || strings.HasPrefix(n, "loop")
}

func countConns(nets []model.NetworkSnap) (est, listen, total int) {
	for _, n := range nets {
		total++
		switch n.Status {
		case "ESTABLISHED":
			est++
		case "LISTEN":
			listen++
		}
	}
	return
}

func (c *Collector) disks() []model.DiskSnap {
	parts, err := disk.Partitions(false)
	if err != nil {
		return nil
	}
	out := make([]model.DiskSnap, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		if seen[p.Mountpoint] || skipMount(p.Mountpoint, p.Fstype) {
			continue
		}
		usage, err := disk.Usage(p.Mountpoint)
		if err != nil {
			continue
		}
		seen[p.Mountpoint] = true
		out = append(out, model.DiskSnap{
			MountPoint: p.Mountpoint,
			Device:     p.Device,
			FSType:     p.Fstype,
			Total:      usage.Total,
			Used:       usage.Used,
			Free:       usage.Free,
			Percent:    usage.UsedPercent,
		})
	}
	return out
}

func skipMount(mount, fstype string) bool {
	fstype = strings.ToLower(fstype)
	for _, p := range []string{"tmpfs", "devfs", "devtmpfs", "proc", "sysfs", "cgroup", "overlay", "squashfs", "autofs"} {
		if fstype == p {
			return true
		}
	}
	for _, p := range []string{"/System/Volumes/Preboot", "/System/Volumes/VM", "/System/Volumes/Update", "/private/var/vm"} {
		if strings.HasPrefix(mount, p) {
			return true
		}
	}
	return false
}

func (c *Collector) processes(blacklist []string) []model.ProcessSnap {
	procs, err := process.Processes()
	if err != nil {
		return nil
	}
	bl := normalizeSet(blacklist)
	out := make([]model.ProcessSnap, 0, len(procs))
	for _, p := range procs {
		_, _ = p.CPUPercent()
	}
	time.Sleep(120 * time.Millisecond)

	for _, p := range procs {
		name, _ := p.Name()
		if name == "" {
			continue
		}
		cpuP, _ := p.CPUPercent()
		memP, _ := p.MemoryPercent()
		mi, _ := p.MemoryInfo()
		statusArr, _ := p.Status()
		status := ""
		if len(statusArr) > 0 {
			status = statusArr[0]
		}
		user, _ := p.Username()
		ct, _ := p.CreateTime()
		cmd, _ := p.Cmdline()
		rss := uint64(0)
		if mi != nil {
			rss = mi.RSS
		}
		item := model.ProcessSnap{
			PID:        p.Pid,
			Name:       name,
			Username:   user,
			CPUPercent: cpuP,
			MemPercent: float64(memP),
			MemRSS:     rss,
			Status:     status,
			Cmdline:    trimCmd(cmd),
			CreateTime: ct,
		}
		if matchName(name, bl) {
			item.Blacklisted = true
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Blacklisted != out[j].Blacklisted {
			return out[i].Blacklisted
		}
		return out[i].CPUPercent > out[j].CPUPercent
	})
	if len(out) > c.TopN {
		kept := make([]model.ProcessSnap, 0, c.TopN)
		for _, p := range out {
			if p.Blacklisted {
				kept = append(kept, p)
			}
		}
		for _, p := range out {
			if len(kept) >= c.TopN {
				break
			}
			if !p.Blacklisted {
				kept = append(kept, p)
			}
		}
		out = kept
	}
	return out
}

func (c *Collector) networks() []model.NetworkSnap {
	conns, err := gnet.Connections("inet")
	if err != nil {
		return nil
	}
	out := make([]model.NetworkSnap, 0, 80)
	for i, cn := range conns {
		if i >= 120 {
			break
		}
		if cn.Status != "ESTABLISHED" && cn.Status != "LISTEN" {
			continue
		}
		family := "ipv4"
		if cn.Family == 10 || cn.Family == 28 {
			family = "ipv6"
		}
		typ := "tcp"
		if cn.Type == 2 {
			typ = "udp"
		}
		procName := ""
		if cn.Pid > 0 {
			if p, err := process.NewProcess(cn.Pid); err == nil {
				procName, _ = p.Name()
			}
		}
		rIP := cn.Raddr.IP
		remoteHost := ""
		if cn.Status == "ESTABLISHED" && rIP != "" && rIP != "0.0.0.0" && rIP != "::" {
			remoteHost = c.lookupHost(rIP)
		}
		out = append(out, model.NetworkSnap{
			FD:         cn.Fd,
			Family:     family,
			Type:       typ,
			LAddr:      fmt.Sprintf("%s:%d", cn.Laddr.IP, cn.Laddr.Port),
			RAddr:      fmt.Sprintf("%s:%d", rIP, cn.Raddr.Port),
			RemoteHost: remoteHost,
			Status:     cn.Status,
			PID:        cn.Pid,
			Process:    procName,
		})
		if len(out) >= 80 {
			break
		}
	}
	return out
}

func (c *Collector) lookupHost(ip string) string {
	c.dnsMu.Lock()
	if e, ok := c.dnsCache[ip]; ok && time.Since(e.at) < 10*time.Minute {
		c.dnsMu.Unlock()
		return e.host
	}
	c.dnsMu.Unlock()

	host := ""
	names, err := net.LookupAddr(ip)
	if err == nil && len(names) > 0 {
		host = strings.TrimSuffix(strings.ToLower(names[0]), ".")
	}

	c.dnsMu.Lock()
	c.dnsCache[ip] = dnsCacheEntry{host: host, at: time.Now()}
	if len(c.dnsCache) > 500 {
		cutoff := time.Now().Add(-30 * time.Minute)
		for k, v := range c.dnsCache {
			if v.at.Before(cutoff) {
				delete(c.dnsCache, k)
			}
		}
	}
	c.dnsMu.Unlock()
	return host
}

func primaryIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil {
				continue
			}
			return ip4.String()
		}
	}
	return ""
}

func normalizeSet(items []string) map[string]struct{} {
	m := make(map[string]struct{}, len(items))
	for _, it := range items {
		it = strings.ToLower(strings.TrimSpace(it))
		if it != "" {
			m[it] = struct{}{}
		}
	}
	return m
}

func matchName(name string, set map[string]struct{}) bool {
	n := strings.ToLower(name)
	if _, ok := set[n]; ok {
		return true
	}
	for k := range set {
		if strings.Contains(n, k) {
			return true
		}
	}
	return false
}

func trimCmd(s string) string {
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}
