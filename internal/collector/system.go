package collector

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func collectSystem() SystemStats {
	s := SystemStats{}

	// Hostname
	s.Hostname, _ = os.Hostname()

	// Uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 1 {
			s.Uptime, _ = strconv.ParseFloat(fields[0], 64)
			s.UptimeHuman = formatUptime(s.Uptime)
		}
	}

	// Entropy
	if data, err := os.ReadFile("/proc/sys/kernel/random/entropy_avail"); err == nil {
		s.Entropy, _ = strconv.Atoi(strings.TrimSpace(string(data)))
	}

	// Clock source
	if data, err := os.ReadFile("/sys/devices/system/clocksource/clocksource0/current_clocksource"); err == nil {
		s.ClockSource = strings.TrimSpace(string(data))
	}

	// Clock sync - check via /sys/class/ptp or adjtimex status
	s.ClockSync = checkClockSync()

	// Users from /var/run/utmp
	s.Users = parseUtmp()
	s.UserCount = len(s.Users)

	return s
}

func checkClockSync() bool {
	// Try timedatectl equivalent: check /run/systemd/timesync/synchronized
	if _, err := os.Stat("/run/systemd/timesync/synchronized"); err == nil {
		return true
	}
	// As a fallback, check if chrony or ntp socket exists
	for _, path := range []string{
		"/var/run/chrony/chronyd.pid",
		"/var/run/ntpd.pid",
		"/run/chrony/chronyd.pid",
		"/run/ntpd.pid",
	} {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// parseUtmp reads /var/run/utmp to get logged-in users.
// Simplified parsing — utmp has a fixed record format.
func parseUtmp() []User {
	f, err := os.Open("/var/run/utmp")
	if err != nil {
		// Fallback: read from 'who' style info in /proc
		return parseUsersFromProc()
	}
	defer f.Close()

	var users []User
	// utmp record size on x86_64 Linux is 384 bytes
	const recordSize = 384
	const utTypeOffset = 0
	const utUserOffset = 8
	const utLineOffset = 44
	const utHostOffset = 76
	const userProcess = 7

	buf := make([]byte, recordSize)
	for {
		n, err := f.Read(buf)
		if n < recordSize || err != nil {
			break
		}

		utType := int32(buf[utTypeOffset]) | int32(buf[utTypeOffset+1])<<8 |
			int32(buf[utTypeOffset+2])<<16 | int32(buf[utTypeOffset+3])<<24
		if utType != userProcess {
			continue
		}

		name := strings.TrimRight(string(buf[utUserOffset:utUserOffset+32]), "\x00")
		terminal := strings.TrimRight(string(buf[utLineOffset:utLineOffset+32]), "\x00")
		host := strings.TrimRight(string(buf[utHostOffset:utHostOffset+256]), "\x00")

		if name != "" {
			users = append(users, User{
				Name:     name,
				Terminal: terminal,
				Host:     host,
			})
		}
	}
	return users
}

func parseUsersFromProc() []User {
	// Fallback: count loginuid files
	var users []User
	seen := make(map[string]bool)

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid := entry.Name()
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}

		statusPath := fmt.Sprintf("/proc/%s/status", pid)
		f, err := os.Open(statusPath)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "Uid:") {
				fields := strings.Fields(line)
				if len(fields) >= 2 && fields[1] != "0" && !seen[fields[1]] {
					seen[fields[1]] = true
				}
			}
		}
		f.Close()
	}

	// This fallback doesn't give us usernames without cgo, just return count
	return users
}
