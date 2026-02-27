package collector

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

type netRaw struct {
	rxBytes, txBytes uint64
	rxPkts, txPkts   uint64
	rxErrs, txErrs   uint64
	rxDrop, txDrop   uint64
}

func parseNetDev() map[string]netRaw {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return nil
	}
	defer f.Close()

	result := make(map[string]netRaw)
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // skip header lines
		}
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}
		n := netRaw{}
		n.rxBytes, _ = strconv.ParseUint(fields[0], 10, 64)
		n.rxPkts, _ = strconv.ParseUint(fields[1], 10, 64)
		n.rxErrs, _ = strconv.ParseUint(fields[2], 10, 64)
		n.rxDrop, _ = strconv.ParseUint(fields[3], 10, 64)
		n.txBytes, _ = strconv.ParseUint(fields[8], 10, 64)
		n.txPkts, _ = strconv.ParseUint(fields[9], 10, 64)
		n.txErrs, _ = strconv.ParseUint(fields[10], 10, 64)
		n.txDrop, _ = strconv.ParseUint(fields[11], 10, 64)
		result[name] = n
	}
	return result
}

func (c *Collector) collectNetwork(elapsed float64) NetworkStats {
	current := parseNetDev()
	stats := NetworkStats{}

	for name, cur := range current {
		iface := NetInterface{
			Name:    name,
			RxBytes: cur.rxBytes,
			TxBytes: cur.txBytes,
			RxPkts:  cur.rxPkts,
			TxPkts:  cur.txPkts,
			RxErrs:  cur.rxErrs,
			TxErrs:  cur.txErrs,
			RxDrop:  cur.rxDrop,
			TxDrop:  cur.txDrop,
		}

		if prev, ok := c.prevNet[name]; ok && elapsed > 0 {
			rxDelta := cur.rxBytes - prev.rxBytes
			txDelta := cur.txBytes - prev.txBytes
			iface.RxMbps = float64(rxDelta) * 8.0 / elapsed / 1_000_000.0
			iface.TxMbps = float64(txDelta) * 8.0 / elapsed / 1_000_000.0
			rxPktDelta := cur.rxPkts - prev.rxPkts
			txPktDelta := cur.txPkts - prev.txPkts
			iface.RxPPS = float64(rxPktDelta) / elapsed
			iface.TxPPS = float64(txPktDelta) / elapsed
		}

		stats.Interfaces = append(stats.Interfaces, iface)
	}

	c.prevNet = current

	// Parse socket stats
	stats.Sockets = parseSocketStats()
	stats.TCP = parseSNMP("Tcp")
	stats.UDP = parseSNMP("Udp")
	stats.TCP6 = parseSNMP6("Tcp6")
	stats.UDP6 = parseSNMP6("Udp6")

	return stats
}

func parseSocketStats() SocketStats {
	ss := SocketStats{}
	// IPv4
	parseSockstatFile("/proc/net/sockstat", &ss)
	// IPv6
	parseSockstatFile6("/proc/net/sockstat6", &ss)
	return ss
}

func parseSockstatFile(path string, ss *SocketStats) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		switch fields[0] {
		case "TCP:":
			for i := 1; i+1 < len(fields); i += 2 {
				val, _ := strconv.Atoi(fields[i+1])
				switch fields[i] {
				case "inuse":
					ss.TCPInUse = val
				case "orphan":
					ss.TCPOrphan = val
				case "tw":
					ss.TCPTw = val
				case "alloc":
					ss.TCPAlloc = val
				case "mem":
					ss.TCPMem = val
				}
			}
		case "UDP:":
			for i := 1; i+1 < len(fields); i += 2 {
				val, _ := strconv.Atoi(fields[i+1])
				switch fields[i] {
				case "inuse":
					ss.UDPInUse = val
				case "mem":
					ss.UDPMem = val
				}
			}
		case "RAW:":
			for i := 1; i+1 < len(fields); i += 2 {
				val, _ := strconv.Atoi(fields[i+1])
				if fields[i] == "inuse" {
					ss.RawInUse = val
				}
			}
		case "FRAG:":
			for i := 1; i+1 < len(fields); i += 2 {
				val, _ := strconv.Atoi(fields[i+1])
				if fields[i] == "inuse" {
					ss.FragInUse = val
				}
			}
		}
	}
}

func parseSockstatFile6(path string, ss *SocketStats) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		switch fields[0] {
		case "TCP6:":
			for i := 1; i+1 < len(fields); i += 2 {
				val, _ := strconv.Atoi(fields[i+1])
				if fields[i] == "inuse" {
					ss.TCP6InUse = val
				}
			}
		case "UDP6:":
			for i := 1; i+1 < len(fields); i += 2 {
				val, _ := strconv.Atoi(fields[i+1])
				if fields[i] == "inuse" {
					ss.UDP6InUse = val
				}
			}
		case "RAW6:":
			for i := 1; i+1 < len(fields); i += 2 {
				val, _ := strconv.Atoi(fields[i+1])
				if fields[i] == "inuse" {
					ss.Raw6InUse = val
				}
			}
		case "FRAG6:":
			for i := 1; i+1 < len(fields); i += 2 {
				val, _ := strconv.Atoi(fields[i+1])
				if fields[i] == "inuse" {
					ss.Frag6InUse = val
				}
			}
		}
	}
}

func parseSNMP(proto string) NetProtoStats {
	f, err := os.Open("/proc/net/snmp")
	if err != nil {
		return NetProtoStats{}
	}
	defer f.Close()

	ns := NetProtoStats{}
	scanner := bufio.NewScanner(f)
	var headerFields []string
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		prefix := strings.TrimSuffix(fields[0], ":")
		if prefix != proto {
			if prefix == proto {
				headerFields = fields[1:]
			}
			continue
		}
		if headerFields == nil {
			headerFields = fields[1:]
			continue
		}
		// This is the values line
		values := fields[1:]
		for i, hdr := range headerFields {
			if i >= len(values) {
				break
			}
			val, _ := strconv.ParseUint(values[i], 10, 64)
			switch hdr {
			case "ActiveOpens":
				ns.ActiveOpens = val
			case "PassiveOpens":
				ns.PassiveOpens = val
			case "CurrEstab":
				ns.CurrEstab = val
			case "InSegs":
				ns.InSegs = val
			case "OutSegs":
				ns.OutSegs = val
			case "InErrs":
				ns.InErrs = val
			case "OutRsts":
				ns.OutRsts = val
			case "InDatagrams":
				ns.InDatagrams = val
			case "OutDatagrams":
				ns.OutDatagrams = val
			}
		}
		headerFields = nil
	}
	return ns
}

func parseSNMP6(proto string) NetProtoStats {
	f, err := os.Open("/proc/net/snmp6")
	if err != nil {
		return NetProtoStats{}
	}
	defer f.Close()

	ns := NetProtoStats{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		val, _ := strconv.ParseUint(fields[1], 10, 64)
		switch fields[0] {
		case proto + "InSegs":
			ns.InSegs = val
		case proto + "OutSegs":
			ns.OutSegs = val
		case proto + "ActiveOpens":
			ns.ActiveOpens = val
		case proto + "PassiveOpens":
			ns.PassiveOpens = val
		case proto + "CurrEstab":
			ns.CurrEstab = val
		case proto + "InDatagrams":
			ns.InDatagrams = val
		case proto + "OutDatagrams":
			ns.OutDatagrams = val
		}
	}
	return ns
}
