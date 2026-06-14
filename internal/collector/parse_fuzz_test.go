package collector

import (
	"encoding/json"
	"strconv"
	"testing"

	"kula/internal/config"
)

// Fuzz coverage for parsers that consume input from outside the agent's own
// trust boundary: the nginx/apache status pages fetched over HTTP from a
// possibly-misbehaving (or attacker-influenced) endpoint, the no-alloc integer
// parser on the /proc hot path, and the JSON custom-metrics protocol that any
// local process can speak over the Unix socket. None of these may panic.

// FuzzParseNginxStatus feeds arbitrary bodies (and elapsed values, including
// negative/NaN-adjacent) to the stub_status parser.
func FuzzParseNginxStatus(f *testing.F) {
	f.Add("Active connections: 4\nserver accepts handled requests\n 7 7 7 \nReading: 0 Writing: 3 Waiting: 1\n", 1.0)
	f.Add("Active connections: 1\nx\n 1 1 1\nReading: 0 Writing: 0 Waiting: 0", 0.0)
	f.Add("", -1.0)
	f.Add("Active connections: 999999999999999999999999999999\n\n\n", 0.5)
	f.Add("Active connections: 5\nserver\n a b c\nReading: x Writing: y Waiting: z", 2.0)

	f.Fuzz(func(t *testing.T, body string, elapsed float64) {
		c := &Collector{}
		// Two calls: the second exercises the prevNginx delta/rate path with a
		// populated previous sample, which is where counter-reset and divide
		// logic lives.
		_ = c.parseNginxStatus(body, elapsed)
		_ = c.parseNginxStatus(body, elapsed)
	})
}

// FuzzParseApache2Status feeds arbitrary bodies to the mod_status parser,
// including the scoreboard accumulation and key/value paths.
func FuzzParseApache2Status(f *testing.F) {
	f.Add("Total Accesses: 100\nTotal kBytes: 200\nBusyWorkers: 2\nIdleWorkers: 8\nScoreboard: __WW.._SR\n", 1.0)
	f.Add("Scoreboard: \n", 0.0)
	f.Add("Total Accesses:\nBusyWorkers: notanumber\n", -1.0)
	f.Add(":\n:\n:::\nScoreboard", 1.0)
	f.Add("", 0.0)

	f.Fuzz(func(t *testing.T, body string, elapsed float64) {
		c := &Collector{}
		_ = c.parseApache2Status(body, elapsed)
		_ = c.parseApache2Status(body, elapsed)
	})
}

// FuzzParseUintBytes asserts the no-alloc /proc integer parser never panics and
// stays faithful to strconv for every in-range, all-digit input — the only
// shape it ever sees on the hot path.
func FuzzParseUintBytes(f *testing.F) {
	f.Add([]byte("12345"))
	f.Add([]byte(""))
	f.Add([]byte("0"))
	f.Add([]byte("007"))
	f.Add([]byte("18446744073709551615")) // math.MaxUint64
	f.Add([]byte("abc"))
	f.Add([]byte("12 34"))
	f.Add([]byte("-1"))

	f.Fuzz(func(t *testing.T, b []byte) {
		got := parseUintBytes(b)

		allDigits := len(b) > 0
		for _, ch := range b {
			if ch < '0' || ch > '9' {
				allDigits = false
				break
			}
		}

		if !allDigits {
			// Contract: empty or any non-digit byte yields 0.
			if got != 0 {
				t.Fatalf("parseUintBytes(%q) = %d, want 0 for non-all-digit input", b, got)
			}
			return
		}

		// All-digit and fits in uint64: must equal strconv exactly. (Inputs that
		// overflow uint64 wrap by design — /proc counters never reach that — so
		// they are intentionally not asserted here.)
		if want, err := strconv.ParseUint(string(b), 10, 64); err == nil && got != want {
			t.Fatalf("parseUintBytes(%q) = %d, strconv = %d", b, got, want)
		}
	})
}

// FuzzCustomMessage replays the exact decode path of the custom-metrics Unix
// socket: a single JSON line is unmarshalled into customMessage and, when
// well-formed, fed to processMessage. Any local process can send these bytes,
// so a malformed or hostile payload must be handled without panicking.
func FuzzCustomMessage(f *testing.F) {
	f.Add([]byte(`{"custom":{"cpu_fans":[{"fan1":4423},{"fan2":8512}]}}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"custom":null}`))
	f.Add([]byte(`{"custom":{"unconfigured":[{"x":1}]}}`))
	f.Add([]byte(`{"custom":{"cpu_fans":[]}}`))
	f.Add([]byte(`{"custom":{"cpu_fans":[{}]}}`))
	f.Add([]byte(`{"custom":{"cpu_fans":[{"fan1":1},{"fan1":2}]}}`)) // repeated name -> deduped
	f.Add([]byte(`not json at all`))

	configs := map[string][]config.CustomMetricConfig{
		"cpu_fans": {{Name: "fan1"}, {Name: "fan2"}},
		"room":     {{Name: "ambient"}},
	}
	set, order := indexConfigs(configs)

	f.Fuzz(func(t *testing.T, data []byte) {
		cc := &customCollector{
			latest:      make(map[string][]CustomMetricValue),
			configSet:   set,
			configOrder: order,
		}
		var msg customMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			return // invalid JSON: handleConn skips it
		}
		if msg.Custom == nil {
			return
		}
		cc.processMessage(msg.Custom) // must not panic
		_ = cc.Latest()               // exercise the defensive-copy read path
	})
}
