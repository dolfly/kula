package collector

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type mysqlRaw struct {
	questions       int64
	comSelect       int64
	comInsert       int64
	comUpdate       int64
	comDelete       int64
	slowQueries     int64
	innodbBPReads   int64
	innodbBPRequests int64
	tableLocksWaited int64
	rowLockWaits    int64
}

type mysqlCollector struct {
	dsn          string
	db           *sql.DB
	dbName       string
	prev         mysqlRaw
	debug        bool
	wasConnected bool
	lastErr      string
	timeout      time.Duration
}

func newMysqlCollector(host string, port int, user, password, dbname string, debug bool, timeout time.Duration) *mysqlCollector {
	var dsn string
	if port == 0 {
		dsn = fmt.Sprintf("%s:%s@unix(%s)/%s", user, password, host, dbname)
	} else {
		dsn = fmt.Sprintf("%s:%s@tcp(%s:%d)/%s", user, password, host, port, dbname)
	}
	dsn += "?timeout=5s&readTimeout=5s"

	return &mysqlCollector{
		dsn:     dsn,
		dbName:  dbname,
		debug:   debug,
		timeout: timeout,
	}
}

func (mc *mysqlCollector) connect() error {
	if mc.db != nil {
		ctx, cancel := context.WithTimeout(context.Background(), mc.timeout)
		defer cancel()
		if err := mc.db.PingContext(ctx); err == nil {
			return nil
		}
		_ = mc.db.Close()
		mc.db = nil
		mc.prev = mysqlRaw{}
		mc.lastErr = ""
		if mc.wasConnected {
			log.Printf("[mysql] connection to %q lost, will retry", mc.dbName)
			mc.wasConnected = false
		}
	}

	db, err := sql.Open("mysql", mc.dsn)
	if err != nil {
		errStr := err.Error()
		if errStr != mc.lastErr {
			log.Printf("[mysql] failed to open database %q: %v", mc.dbName, err)
			mc.lastErr = errStr
		}
		return fmt.Errorf("mysql open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), mc.timeout)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		errStr := err.Error()
		if errStr != mc.lastErr {
			log.Printf("[mysql] failed to ping database %q: %v", mc.dbName, err)
			mc.lastErr = errStr
		}
		return fmt.Errorf("mysql ping: %w", err)
	}
	mc.db = db
	mc.lastErr = ""
	if !mc.wasConnected {
		log.Printf("[mysql] connected to database %q", mc.dbName)
		mc.wasConnected = true
	}
	return nil
}

func (mc *mysqlCollector) Close() {
	if mc.db != nil {
		_ = mc.db.Close()
	}
}

func (c *Collector) collectMysql(elapsed float64) *MysqlStats {
	if c.myCollector == nil {
		return nil
	}

	if err := c.myCollector.connect(); err != nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.collCfg.Interval)
	defer cancel()

	stats := &MysqlStats{}

	rows, err := c.myCollector.db.QueryContext(ctx, "SHOW GLOBAL STATUS")
	if err != nil {
		c.debugf("[mysql] SHOW GLOBAL STATUS error: %v", err)
		return nil
	}

	statusVars := make(map[string]string)
	for rows.Next() {
		var name, val string
		if err := rows.Scan(&name, &val); err != nil {
			_ = rows.Close()
			return nil
		}
		statusVars[name] = val
	}
	_ = rows.Close()

	if err := rows.Err(); err != nil {
		c.debugf("[mysql] SHOW GLOBAL STATUS iteration error: %v", err)
		return nil
	}

	parseI := func(key string) int {
		v, _ := strconv.Atoi(statusVars[key])
		return v
	}
	parseI64 := func(key string) int64 {
		v, _ := strconv.ParseInt(statusVars[key], 10, 64)
		return v
	}

	stats.ThreadsConnected = parseI("Threads_connected")
	stats.ThreadsRunning = parseI("Threads_running")
	stats.ThreadsCached = parseI("Threads_cached")

	var cur mysqlRaw
	cur.questions = parseI64("Questions")
	cur.comSelect = parseI64("Com_select")
	cur.comInsert = parseI64("Com_insert")
	cur.comUpdate = parseI64("Com_update")
	cur.comDelete = parseI64("Com_delete")
	cur.slowQueries = parseI64("Slow_queries")
	cur.innodbBPReads = parseI64("Innodb_buffer_pool_reads")
	cur.innodbBPRequests = parseI64("Innodb_buffer_pool_read_requests")
	cur.tableLocksWaited = parseI64("Table_locks_waited")
	cur.rowLockWaits = parseI64("Innodb_row_lock_waits")

	c.debugf("[mysql] raw: questions=%d, select=%d, insert=%d, update=%d, delete=%d, slow=%d, bp_reads=%d, bp_reqs=%d",
		cur.questions, cur.comSelect, cur.comInsert, cur.comUpdate, cur.comDelete,
		cur.slowQueries, cur.innodbBPReads, cur.innodbBPRequests)

	var maxConnsStr string
	if err := c.myCollector.db.QueryRowContext(ctx, "SELECT @@max_connections").Scan(&maxConnsStr); err == nil {
		if v, err := strconv.Atoi(maxConnsStr); err == nil {
			stats.MaxConnections = v
		}
	}

	c.myCollector.calculateStats(stats, cur, elapsed)

	return stats
}

func (mc *mysqlCollector) calculateStats(stats *MysqlStats, cur mysqlRaw, elapsed float64) {
	if mc.prev.questions > 0 && elapsed > 0 {
		rate := func(cur, prev int64) float64 {
			if cur < prev {
				return 0
			}
			return round2(float64(cur-prev) / elapsed)
		}
		stats.QueriesPS       = rate(cur.questions,        mc.prev.questions)
		stats.ComSelectPS     = rate(cur.comSelect,        mc.prev.comSelect)
		stats.ComInsertPS     = rate(cur.comInsert,        mc.prev.comInsert)
		stats.ComUpdatePS     = rate(cur.comUpdate,        mc.prev.comUpdate)
		stats.ComDeletePS     = rate(cur.comDelete,        mc.prev.comDelete)
		stats.SlowQueriesPS   = rate(cur.slowQueries,      mc.prev.slowQueries)
		stats.InnodbBPReadsPS = rate(cur.innodbBPReads,    mc.prev.innodbBPReads)
		stats.TableLocksWaitedPS = rate(cur.tableLocksWaited, mc.prev.tableLocksWaited)
		stats.RowLockWaitsPS  = rate(cur.rowLockWaits,     mc.prev.rowLockWaits)
	}
	mc.prev = cur

	if cur.innodbBPRequests > 0 {
		hits := cur.innodbBPRequests - cur.innodbBPReads
		stats.InnodbBufferPoolHitPct = round2(float64(hits) / float64(cur.innodbBPRequests) * 100)
	}
}
