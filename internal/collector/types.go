package collector

import "time"

// Sample holds all metrics collected at a single point in time.
type Sample struct {
	Timestamp time.Time `json:"ts"`

	CPU     CPUStats     `json:"cpu"`
	LoadAvg LoadAvg      `json:"lavg"`
	Memory  MemoryStats  `json:"mem"`
	Swap    SwapStats    `json:"swap"`
	Network NetworkStats `json:"net"`
	Disks   DiskStats    `json:"disk"`
	System  SystemStats  `json:"sys"`
	Process ProcessStats `json:"proc"`
	Self    SelfStats    `json:"self"`
}

// CPUStats holds per-core and total CPU usage percentages.
type CPUStats struct {
	Total CPUCoreStats   `json:"total"`
	Cores []CPUCoreStats `json:"cores"`
}

type CPUCoreStats struct {
	ID      string  `json:"id"`
	User    float64 `json:"user"`
	Nice    float64 `json:"nice"`
	System  float64 `json:"system"`
	Idle    float64 `json:"idle"`
	IOWait  float64 `json:"iowait"`
	IRQ     float64 `json:"irq"`
	SoftIRQ float64 `json:"softirq"`
	Steal   float64 `json:"steal"`
	Guest   float64 `json:"guest"`
	GuestNi float64 `json:"guest_nice"`
	Usage   float64 `json:"usage"` // 100 - idle
}

type LoadAvg struct {
	Load1   float64 `json:"load1"`
	Load5   float64 `json:"load5"`
	Load15  float64 `json:"load15"`
	Running int     `json:"running"`
	Total   int     `json:"total"`
}

type MemoryStats struct {
	Total        uint64 `json:"total"`
	Free         uint64 `json:"free"`
	Available    uint64 `json:"available"`
	Used         uint64 `json:"used"`
	Buffers      uint64 `json:"buffers"`
	Cached       uint64 `json:"cached"`
	SReclaimable uint64 `json:"sreclaimable"`
	SUnreclaim   uint64 `json:"sunreclaim"`
	Shmem        uint64 `json:"shmem"`
	Dirty        uint64 `json:"dirty"`
	Writeback    uint64 `json:"writeback"`
	Mapped       uint64 `json:"mapped"`
	UsedPercent  float64 `json:"used_pct"`
}

type SwapStats struct {
	Total       uint64  `json:"total"`
	Free        uint64  `json:"free"`
	Used        uint64  `json:"used"`
	Cached      uint64  `json:"cached"`
	UsedPercent float64 `json:"used_pct"`
}

type NetworkStats struct {
	Interfaces []NetInterface `json:"ifaces"`
	TCP        NetProtoStats  `json:"tcp"`
	UDP        NetProtoStats  `json:"udp"`
	TCP6       NetProtoStats  `json:"tcp6"`
	UDP6       NetProtoStats  `json:"udp6"`
	Sockets    SocketStats    `json:"sockets"`
}

type NetInterface struct {
	Name    string  `json:"name"`
	RxBytes uint64  `json:"rx_bytes"`
	TxBytes uint64  `json:"tx_bytes"`
	RxMbps  float64 `json:"rx_mbps"`
	TxMbps  float64 `json:"tx_mbps"`
	RxPkts  uint64  `json:"rx_pkts"`
	TxPkts  uint64  `json:"tx_pkts"`
	RxPPS   float64 `json:"rx_pps"`
	TxPPS   float64 `json:"tx_pps"`
	RxErrs  uint64  `json:"rx_errs"`
	TxErrs  uint64  `json:"tx_errs"`
	RxDrop  uint64  `json:"rx_drop"`
	TxDrop  uint64  `json:"tx_drop"`
}

type NetProtoStats struct {
	ActiveOpens  uint64 `json:"active_opens"`
	PassiveOpens uint64 `json:"passive_opens"`
	CurrEstab    uint64 `json:"curr_estab"`
	InSegs       uint64 `json:"in_segs"`
	OutSegs      uint64 `json:"out_segs"`
	InErrs       uint64 `json:"in_errs"`
	OutRsts      uint64 `json:"out_rsts"`
	InDatagrams  uint64 `json:"in_dgrams"`
	OutDatagrams uint64 `json:"out_dgrams"`
}

type SocketStats struct {
	TCPInUse   int `json:"tcp_inuse"`
	TCPOrphan  int `json:"tcp_orphan"`
	TCPTw      int `json:"tcp_tw"`
	TCPAlloc   int `json:"tcp_alloc"`
	TCPMem     int `json:"tcp_mem"`
	UDPInUse   int `json:"udp_inuse"`
	UDPMem     int `json:"udp_mem"`
	TCP6InUse  int `json:"tcp6_inuse"`
	UDP6InUse  int `json:"udp6_inuse"`
	RawInUse   int `json:"raw_inuse"`
	Raw6InUse  int `json:"raw6_inuse"`
	FragInUse  int `json:"frag_inuse"`
	Frag6InUse int `json:"frag6_inuse"`
}

type DiskStats struct {
	Devices     []DiskDevice    `json:"devices"`
	FileSystems []FileSystemInfo `json:"filesystems"`
}

type DiskDevice struct {
	Name         string  `json:"name"`
	ReadsPerSec  float64 `json:"reads_ps"`
	WritesPerSec float64 `json:"writes_ps"`
	ReadBytesPS  float64 `json:"read_bps"`
	WriteBytesPS float64 `json:"write_bps"`
	IOInProgress uint64  `json:"io_in_progress"`
	IOTime       uint64  `json:"io_time_ms"`
	WeightedIO   uint64  `json:"weighted_io_ms"`
	Utilization  float64 `json:"util_pct"`
}

type FileSystemInfo struct {
	Device     string  `json:"device"`
	MountPoint string  `json:"mount"`
	FSType     string  `json:"fstype"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Available  uint64  `json:"available"`
	UsedPct    float64 `json:"used_pct"`
	InodesTotal uint64 `json:"inodes_total"`
	InodesUsed  uint64 `json:"inodes_used"`
	InodesFree  uint64 `json:"inodes_free"`
}

type SystemStats struct {
	Hostname    string  `json:"hostname"`
	Uptime      float64 `json:"uptime_sec"`
	UptimeHuman string  `json:"uptime_human"`
	Entropy     int     `json:"entropy"`
	ClockSync   bool    `json:"clock_synced"`
	ClockSource string  `json:"clock_source"`
	Users       []User  `json:"users"`
	UserCount   int     `json:"user_count"`
}

type User struct {
	Name     string `json:"name"`
	Terminal string `json:"terminal"`
	Host     string `json:"host"`
}

type ProcessStats struct {
	Total    int `json:"total"`
	Running  int `json:"running"`
	Sleeping int `json:"sleeping"`
	Stopped  int `json:"stopped"`
	Zombie   int `json:"zombie"`
	Blocked  int `json:"blocked"`
	Idle     int `json:"idle"`
	Other    int `json:"other"`
	Threads  int `json:"threads"`
}

type SelfStats struct {
	CPUPercent float64 `json:"cpu_pct"`
	MemRSS     uint64  `json:"mem_rss"`
	MemVMS     uint64  `json:"mem_vms"`
	NumThreads int     `json:"threads"`
	FDs        int     `json:"fds"`
}
