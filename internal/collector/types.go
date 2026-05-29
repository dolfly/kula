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
	GPU     []GPUStats         `json:"gpu,omitempty"`
	PSU     []PowerSupplyStats `json:"psu,omitempty"`
	Apps    ApplicationsStats  `json:"apps,omitempty"`
}

// CPUStats holds per-core and total CPU usage percentages.
type CPUStats struct {
	Total       CPUCoreStats    `json:"total"`
	NumCores    int             `json:"num_cores"`
	Temperature float64         `json:"temp,omitempty"`
	Sensors     []CPUTempSensor `json:"sensors,omitempty"`
}

type CPUTempSensor struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type CPUCoreStats struct {
	User    float64 `json:"user"`
	System  float64 `json:"system"`
	IOWait  float64 `json:"iowait"`
	IRQ     float64 `json:"irq"`
	SoftIRQ float64 `json:"softirq"`
	Steal   float64 `json:"steal"`
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
	Total       uint64  `json:"total"`
	Free        uint64  `json:"free"`
	Available   uint64  `json:"available"`
	Used        uint64  `json:"used"`
	Buffers     uint64  `json:"buffers"`
	Cached      uint64  `json:"cached"`
	Shmem       uint64  `json:"shmem"`
	UsedPercent float64 `json:"used_pct"`
}

type SwapStats struct {
	Total       uint64  `json:"total"`
	Free        uint64  `json:"free"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"used_pct"`
}

type NetworkStats struct {
	Interfaces []NetInterface `json:"ifaces"`
	TCP        TCPStats       `json:"tcp"`
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

// TCPStats holds key TCP protocol counters.
// CurrEstab is a gauge. InErrs, OutRsts, and Retrans are per-second rates (delta/elapsed).
type TCPStats struct {
	CurrEstab uint64  `json:"curr_estab"`
	InErrs    float64 `json:"in_errs_ps"`
	OutRsts   float64 `json:"out_rsts_ps"`
	Retrans   float64 `json:"retrans_ps"`
}

type SocketStats struct {
	TCPInUse int `json:"tcp_inuse"`
	TCPTw    int `json:"tcp_tw"`
	UDPInUse int `json:"udp_inuse"`
}

type DiskStats struct {
	Devices     []DiskDevice     `json:"devices"`
	FileSystems []FileSystemInfo `json:"filesystems"`
}

type DiskDevice struct {
	Name         string           `json:"name"`
	ReadsPerSec  float64          `json:"reads_ps"`
	WritesPerSec float64          `json:"writes_ps"`
	ReadBytesPS  float64          `json:"read_bps"`
	WriteBytesPS float64          `json:"write_bps"`
	Utilization  float64          `json:"util_pct"`
	Temperature  float64          `json:"temp,omitempty"`
	Sensors      []DiskTempSensor `json:"sensors,omitempty"`
}

type DiskTempSensor struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}

type FileSystemInfo struct {
	Device     string  `json:"device"`
	MountPoint string  `json:"mount"`
	FSType     string  `json:"fstype"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Available  uint64  `json:"available"`
	UsedPct    float64 `json:"used_pct"`
}

type SystemStats struct {
	Hostname    string  `json:"hostname"`
	Uptime      float64 `json:"uptime_sec"`
	UptimeHuman string  `json:"uptime_human"`
	Entropy     int     `json:"entropy"`
	ClockSync   bool    `json:"clock_synced"`
	ClockSource string  `json:"clock_source"`
	UserCount   int     `json:"user_count"`
}

type ProcessStats struct {
	Total    int `json:"total"`
	Running  int `json:"running"`
	Sleeping int `json:"sleeping"`
	Zombie   int `json:"zombie"`
	Blocked  int `json:"blocked"`
	Threads  int `json:"threads"`
}

type SelfStats struct {
	CPUPercent float64 `json:"cpu_pct"`
	MemRSS     uint64  `json:"mem_rss"`
	FDs        int     `json:"fds"`
}

type GPUStats struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	Driver      string  `json:"driver"`
	Temperature float64 `json:"temp,omitempty"`
	VRAMUsed    uint64  `json:"vram_used,omitempty"`
	VRAMTotal   uint64  `json:"vram_total,omitempty"`
	VRAMUsedPct float64 `json:"vram_pct,omitempty"`
	LoadPct     float64 `json:"load_pct,omitempty"`
	PowerW      float64 `json:"power_w,omitempty"`
}

// ApplicationsStats holds metrics from external applications.
type ApplicationsStats struct {
	Nginx      *NginxStats                    `json:"nginx,omitempty"`
	Apache2    *Apache2Stats                  `json:"apache2,omitempty"`
	Containers []ContainerStats               `json:"containers,omitempty"`
	Postgres   *PostgresStats                 `json:"postgres,omitempty"`
	Mysql      *MysqlStats                    `json:"mysql,omitempty"`
	Custom     map[string][]CustomMetricValue  `json:"custom,omitempty"`
}

// NginxStats holds metrics parsed from the nginx stub_status module.
type NginxStats struct {
	ActiveConnections int     `json:"active_conn"`
	Accepts           uint64  `json:"accepts"`
	Handled           uint64  `json:"handled"`
	Requests          uint64  `json:"requests"`
	AcceptsPS         float64 `json:"accepts_ps"`
	HandledPS         float64 `json:"handled_ps"`
	RequestsPS        float64 `json:"requests_ps"`
	Reading           int     `json:"reading"`
	Writing           int     `json:"writing"`
	Waiting           int     `json:"waiting"`
}

// Apache2Stats holds metrics parsed from the Apache2 mod_status ?auto endpoint.
type Apache2Stats struct {
	BusyWorkers   int     `json:"busy_workers"`
	IdleWorkers   int     `json:"idle_workers"`
	TotalAccesses uint64  `json:"total_accesses"`
	TotalKBytes   uint64  `json:"total_kbytes"`
	AccessesPS    float64 `json:"accesses_ps"`
	KBytesPS      float64 `json:"kbytes_ps"`
	ReqPerSec     float64 `json:"req_per_sec"`
	BytesPerSec   float64 `json:"bytes_per_sec"`
	BytesPerReq   float64 `json:"bytes_per_req"`
	CPULoad       float64 `json:"cpu_load"`
	Uptime        int64   `json:"uptime"`
	Waiting       int     `json:"waiting"`
	Reading       int     `json:"reading"`
	Sending       int     `json:"sending"`
	Keepalive     int     `json:"keepalive"`
	Starting      int     `json:"starting"`
	DNS           int     `json:"dns"`
	Closing       int     `json:"closing"`
	Logging       int     `json:"logging"`
	Graceful      int     `json:"graceful"`
	IdleCleanup   int     `json:"idle_cleanup"`
	OpenSlots     int     `json:"open_slots"`
}

// ContainerStats holds per-container resource usage metrics.
type ContainerStats struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	CPUPct   float64 `json:"cpu_pct"`
	MemUsed  uint64  `json:"mem_used"`
	MemLimit uint64  `json:"mem_limit"`
	MemPct   float64 `json:"mem_pct"`
	NetRxBPS float64 `json:"net_rx_bps"`
	NetTxBPS float64 `json:"net_tx_bps"`
	DiskRBPS float64 `json:"disk_r_bps"`
	DiskWBPS float64 `json:"disk_w_bps"`
}

// PostgresStats holds PostgreSQL database metrics.
type PostgresStats struct {
	// Connection state (from pg_stat_activity)
	ActiveConns   int `json:"active_conns"`
	IdleConns     int `json:"idle_conns"`
	IdleInTxConns int `json:"idle_in_tx_conns"`
	WaitingConns  int `json:"waiting_conns"`
	MaxConns      int `json:"max_conns"`

	// Transaction throughput (per-second rates from pg_stat_database)
	TxCommitPS   float64 `json:"tx_commit_ps"`
	TxRollbackPS float64 `json:"tx_rollback_ps"`

	// Tuple (row) activity rates
	TupFetchedPS  float64 `json:"tup_fetched_ps"`
	TupReturnedPS float64 `json:"tup_returned_ps"`
	TupInsertedPS float64 `json:"tup_inserted_ps"`
	TupUpdatedPS  float64 `json:"tup_updated_ps"`
	TupDeletedPS  float64 `json:"tup_deleted_ps"`

	// I/O: raw block rates and derived cache hit ratio
	BlksReadPS float64 `json:"blks_read_ps"`
	BlksHitPS  float64 `json:"blks_hit_ps"`
	BlksHitPct float64 `json:"blks_hit_pct"`

	// Locking
	DeadlocksPS float64 `json:"deadlocks_ps"`

	// Table health (from pg_stat_user_tables)
	DeadTuples      int64 `json:"dead_tuples"`
	LiveTuples      int64 `json:"live_tuples"`
	AutovacuumCount int64 `json:"autovacuum_count"`

	// Background writer rates (from pg_stat_bgwriter)
	BufCheckpointPS float64 `json:"buf_checkpoint_ps"`
	BufBackendPS    float64 `json:"buf_backend_ps"`

	// Database size
	DBSizeBytes int64 `json:"db_size_bytes"`

	// Replication. IsInRecovery is true on a standby. ReplicaCount is
	// meaningful on a primary (from pg_stat_replication). The two lag fields
	// are meaningful on a standby; on a primary they are 0.
	IsInRecovery         bool    `json:"is_in_recovery"`
	ReplicaCount         int     `json:"replica_count"`
	ReplicationLagBytes  int64   `json:"repl_lag_bytes"`
	ReplicationLagSeconds float64 `json:"repl_lag_seconds"`
}

// MysqlStats holds MySQL database metrics.
type MysqlStats struct {
	ThreadsConnected int `json:"threads_connected"`
	ThreadsRunning   int `json:"threads_running"`
	ThreadsCached    int `json:"threads_cached"`
	MaxConnections   int `json:"max_conns"`

	QueriesPS     float64 `json:"queries_ps"`
	ComSelectPS   float64 `json:"select_ps"`
	ComInsertPS   float64 `json:"insert_ps"`
	ComUpdatePS   float64 `json:"update_ps"`
	ComDeletePS   float64 `json:"delete_ps"`

	SlowQueriesPS float64 `json:"slow_queries_ps"`

	InnodbBufferPoolHitPct float64 `json:"innodb_buffer_pool_hit_pct"`
	InnodbBPReadsPS        float64 `json:"innodb_bp_reads_ps"`

	TableLocksWaitedPS float64 `json:"table_locks_waited_ps"`
	RowLockWaitsPS     float64 `json:"row_lock_waits_ps"`

	// Replication. ReplicaIORunning/ReplicaSQLRunning come from SHOW REPLICA
	// STATUS (or SHOW SLAVE STATUS) and are false when the server isn't
	// configured as a replica. ReplicaSecondsBehind uses -1 to mean NULL or
	// not-replicating. ReplicaCount is from SHOW REPLICAS / SHOW SLAVE HOSTS
	// and is meaningful on a primary.
	ReplicaIORunning      bool `json:"replica_io_running"`
	ReplicaSQLRunning     bool `json:"replica_sql_running"`
	ReplicaSecondsBehind  int  `json:"replica_seconds_behind"`
	ReplicaCount          int  `json:"replica_count"`

	// Replication error / state. LastIOErrno and LastSQLErrno are the most
	// recent MySQL error codes reported by the IO and SQL threads; 0 means
	// no error. IOState is Slave_IO_State (e.g. "Waiting for master to send
	// event") — useful for distinguishing "stalled but technically running"
	// from "actively waiting for data". Stored capped at 200 bytes.
	LastIOErrno  int    `json:"replica_last_io_errno"`
	LastSQLErrno int    `json:"replica_last_sql_errno"`
	IOState      string `json:"replica_io_state,omitempty"`
}

// PowerSupplyStats holds metrics for a single power supply (battery, mains adapter, UPS).
type PowerSupplyStats struct {
	Name       string  `json:"name"`
	Type       string  `json:"type"`              // "Battery", "Mains", "UPS"
	Status     string  `json:"status"`            // "Charging", "Discharging", "Full", "Not charging"
	Capacity   int     `json:"capacity,omitempty"` // 0-100%
	VoltageV   float64 `json:"voltage_v,omitempty"`
	CurrentA   float64 `json:"current_a,omitempty"`
	PowerW     float64 `json:"power_w,omitempty"`
	EnergyWhNow  float64 `json:"energy_wh_now,omitempty"`
	EnergyWhFull float64 `json:"energy_wh_full,omitempty"`
}

// CustomMetricValue holds a single named metric value from external input.
type CustomMetricValue struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}
