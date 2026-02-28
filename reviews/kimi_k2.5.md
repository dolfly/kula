# Code Review Report: Kula-Szpiegula

**Repository:** https://github.com/c0m4r/kula  
**Project:** Kula-Szpiegula - Lightweight Linux Server Monitoring Tool  
**Language:** Go (43.6%), JavaScript (34.7%), CSS (10.9%), HTML (6.5%)  
**License:** AGPL-3.0  
**Review Date:** March 2026  
**Version Reviewed:** 0.3.1

---

## Executive Summary

Kula-Szpiegula is a lightweight, self-contained Linux server monitoring tool written in Go. It collects system metrics from `/proc` and `/sys` filesystems, stores them in a custom tiered ring-buffer storage engine, and serves them through both a Web UI (HTTP/WebSocket) and a Terminal UI (TUI).

**Overall Assessment:** The codebase demonstrates good architectural separation and practical engineering for a monitoring tool. However, several security, performance, and code quality issues need attention before production deployment.

---

## 1. Code Quality Analysis

### 1.1 Architecture & Design Patterns

**Strengths:**
- ✅ Clean separation of concerns with `internal/` package structure
- ✅ Well-defined interfaces between collector, storage, web, and TUI components
- ✅ Consistent Go project layout following standard conventions
- ✅ Good use of Go's `embed` for static web assets
- ✅ Proper use of `sync.RWMutex` for concurrent access patterns

**Areas for Improvement:**
- ⚠️ The `Collector` struct maintains multiple `prev*` state fields that could be better encapsulated
- ⚠️ Magic numbers scattered throughout (e.g., `60` for aggregation, `3600` for max samples)
- ⚠️ Error handling is inconsistent—some errors are logged, others returned, some silently ignored

### 1.2 Code Style & Maintainability

**Positive Observations:**
- Consistent Go naming conventions (CamelCase for exported, camelCase for unexported)
- Good struct tagging for JSON serialization
- Comprehensive type definitions in `types.go`

**Issues Found:**

```go
// In internal/storage/store.go - Magic numbers without constants
if s.tier1Count >= 60 && len(s.tiers) > 1 {  // What is 60?
const maxSamples = 3600  // Should be configurable
```

```go
// In internal/collector/network.go - Silent error handling
n.rxBytes, _ = strconv.ParseUint(fields[0], 10, 64)  // Errors ignored
```

**Recommendation:** Define constants for all magic numbers and implement proper error handling with at least logging.

### 1.3 Documentation

- ✅ Good README with architecture diagram
- ✅ Example configuration file provided
- ⚠️ Missing Go doc comments for many exported functions
- ⚠️ No architecture decision records (ADRs) for design choices

---

## 2. Security Analysis

### 2.1 Authentication & Authorization

**Critical Issues:**

1. **Session Management Vulnerability** (`internal/web/auth.go`)

```go
func (a *AuthManager) ValidateSession(token string) bool {
    a.mu.RLock()
    defer a.mu.RUnlock()
    sess, ok := a.sessions[token]
    if !ok {
        return false
    }
    if time.Now().After(sess.expiresAt) {
        delete(a.sessions, token)  // ⚠️ DELETION UNDER READ LOCK!
        return false
    }
    return true
}
```

**Severity: HIGH** - The code attempts to delete from a map while holding a read lock (RLock). This causes a runtime panic in Go when the race detector is enabled and is undefined behavior.

**Fix:**
```go
func (a *AuthManager) ValidateSession(token string) bool {
    a.mu.RLock()
    sess, ok := a.sessions[token]
    if !ok {
        a.mu.RUnlock()
        return false
    }
    if time.Now().After(sess.expiresAt) {
        a.mu.RUnlock()
        a.mu.Lock()
        delete(a.sessions, token)
        a.mu.Unlock()
        return false
    }
    defer a.mu.RUnlock()
    return true
}
```

2. **Weak Session Token Generation** (`internal/web/auth.go`)

```go
func generateToken() string {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        panic("crypto/rand.Read failed: " + err.Error())  // ⚠️ Panic on error
    }
    return hex.EncodeToString(b)
}
```

While 32 bytes of entropy is acceptable, the panic on error is concerning. Consider graceful degradation.

3. **No Rate Limiting on Login Endpoint**

The `/api/login` endpoint has no rate limiting, making it vulnerable to brute-force attacks.

**Recommendation:** Implement exponential backoff or account lockout after failed attempts.

### 2.2 Cryptographic Implementation

**Issues with Custom Whirlpool Implementation** (`internal/web/whirlpool.go`):

1. ⚠️ **Custom crypto is discouraged** - The project implements its own Whirlpool hash instead of using standard library functions like `bcrypt`, `scrypt`, or `Argon2`
2. ⚠️ **No key stretching** - Whirlpool alone is insufficient for password hashing; it lacks the computational cost factor needed to resist brute-force attacks
3. ⚠️ **Salt-before-password pattern** - `HashPassword` uses `salt + password` which is less common than `password + salt`

```go
func HashPassword(password, salt string) string {
    data := []byte(salt + password)  // ⚠️ Consider using bcrypt instead
    h := NewWhirlpool()
    h.Write(data)
    return hex.EncodeToString(h.Sum(nil))
}
```

**Recommendation:** Replace custom Whirlpool with `golang.org/x/crypto/bcrypt` or `golang.org/x/crypto/argon2`.

### 2.3 Input Validation

**Issues Found:**

1. **Time Parsing Without Bounds Check** (`internal/web/server.go`)

```go
from, err = time.Parse(time.RFC3339, fromStr)
if err != nil {
    http.Error(w, `{"error":"invalid 'from' time"}`, http.StatusBadRequest)
    return
}
```

No validation on the time range—an attacker could request an extremely large range causing resource exhaustion.

2. **Size Parsing Limited** (`internal/config/config.go`)

```go
func parseSize(s string) (int64, error) {
    var val float64
    var unit string
    _, err := fmt.Sscanf(s, "%f%s", &val, &unit)
    // ...
}
```

No upper bound validation—could allow extremely large allocations.

### 2.4 File System Security

**Issues:**

1. **World-Readable Storage Files** (`internal/storage/tier.go`)

```go
f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)  // ⚠️ World-readable
```

The tier data files are created with `0644` permissions, making them world-readable. This exposes monitoring data to any user on the system.

**Fix:** Use `0600` for sensitive data files.

2. **Directory Traversal Risk** (`internal/storage/store.go`)

```go
path := filepath.Join(cfg.Directory, fmt.Sprintf("tier_%d.dat", i))
```

While `filepath.Join` helps, there's no validation that `cfg.Directory` doesn't contain traversal sequences.

### 2.5 Web Security Headers

**Missing Security Headers:**

The HTTP server doesn't set:
- `X-Content-Type-Options: nosniff`
- `X-Frame-Options: DENY` or `SAMEORIGIN`
- `Content-Security-Policy`
- `X-XSS-Protection`

### 2.6 WebSocket Security

```go
// In internal/web/websocket.go
var msg string
if err := websocket.Message.Receive(conn, &msg); err != nil {
    // ...
}
```

No message size limits—vulnerable to memory exhaustion via large WebSocket messages.

---

## 3. Performance Analysis

### 3.1 Memory Management

**Issues:**

1. **Unbounded Buffer Growth** (`internal/storage/store.go`)

```go
s.tier1Buf = append(s.tier1Buf, sample)  // Grows until 60 samples
```

While the buffer is cleared periodically, there's no protection against memory spikes if collection runs faster than expected.

2. **JSON Encoding Overhead** (`internal/storage/codec.go`)

```go
func encodeSample(s *AggregatedSample) ([]byte, error) {
    return json.Marshal(s)  // ⚠️ Reflection-based, relatively slow
}
```

For high-frequency metrics collection, consider:
- MessagePack encoding
- Protocol Buffers
- Manual binary encoding

3. **WebSocket Broadcast Copying** (`internal/web/server.go`)

```go
func (h *wsHub) broadcast(data []byte) {
    // ...
    select {
    case client.sendCh <- data:  // Sends same slice to all clients
    // ...
    }
}
```

The same byte slice is shared across all clients. While efficient, this could lead to race conditions if any client modifies the data.

### 3.2 CPU Efficiency

**Concerns:**

1. **String Parsing in Hot Path** (`internal/collector/`)

All collectors parse text files (`/proc/stat`, `/proc/meminfo`) on every collection tick. For 1-second intervals, this is acceptable, but:
- Consider caching file handles
- Use `syscall.Mmap` for zero-copy reading
- Pre-allocate buffers

2. **Frequent Memory Allocations**

```go
// In internal/collector/network.go
result := make(map[string]netRaw)  // Allocated every second
```

Consider using `sync.Pool` for reusable buffers.

### 3.3 Storage I/O

**Observations:**

1. **Header Write Frequency** (`internal/storage/tier.go`)

```go
if t.count%10 == 0 {
    return t.writeHeader()  // Sync every 10 writes
}
```

This provides a good balance between durability and performance.

2. **File Growth Not Pre-allocated**

The tier files grow dynamically. Pre-allocating the full size would reduce fragmentation and improve I/O predictability.

### 3.4 Concurrency

**Good Practices:**
- Proper use of channels for WebSocket hub communication
- Mutex protection for shared state
- Non-blocking sends with select

**Issues:**

```go
// In internal/web/server.go
func (h *wsHub) broadcast(data []byte) {
    h.mu.RLock()
    defer h.mu.RUnlock()  // ⚠️ Held during entire broadcast
    // ...
}
```

The read lock is held while iterating all clients. With many clients, this blocks registration/unregistration.

---

## 4. Reliability & Error Handling

### 4.1 Error Handling Issues

1. **Silent Failures in Collectors**

```go
// internal/collector/network.go
n.rxBytes, _ = strconv.ParseUint(fields[0], 10, 64)  // Silent failure
```

Many parsing errors are silently ignored, leading to zero values that could be misinterpreted.

2. **Incomplete Error Propagation**

```go
// internal/storage/tier.go
if err != nil {
    _ = f.Close()  // Error ignored
    return nil, err
}
```

Close errors are frequently ignored, potentially masking resource leaks.

### 4.2 Resource Leak Risks

1. **WebSocket Client Cleanup**

```go
defer func() {
    s.hub.unregCh <- client  // Sent but not confirmed processed
    _ = conn.Close()
}()
```

If the hub's `run()` goroutine exits, unregistration messages are lost.

2. **No Context Cancellation**

The codebase doesn't use `context.Context` for graceful shutdown, making it difficult to:
- Cancel long-running operations
- Implement request timeouts
- Propagate shutdown signals

### 4.3 Data Integrity

**Concerns:**

1. **No Checksums in Storage Format**

The tier file format has no checksums for data integrity verification. Corruption could go undetected.

2. **JSON Serialization Risks**

Using JSON for storage means:
- No schema versioning
- Potential for data loss on field renaming
- Larger storage footprint than binary formats

---

## 5. Testing

**Observations:**
- ✅ Unit tests exist for codec, auth, and storage
- ⚠️ No integration tests for the full pipeline
- ⚠️ No benchmark tests for performance-critical paths
- ⚠️ No fuzzing tests for input validation
- ⚠️ Test coverage appears minimal based on file sizes

---

## 6. Recommendations Summary

### High Priority (Security & Stability)

1. **Fix the RLock/Delete race condition** in `auth.go` - CRITICAL
2. **Replace custom Whirlpool with bcrypt/Argon2** - HIGH
3. **Add rate limiting to login endpoint** - HIGH
4. **Fix file permissions** (0644 → 0600) - HIGH
5. **Add input validation** for time ranges and sizes - HIGH

### Medium Priority (Performance & Quality)

6. **Implement proper context handling** for graceful shutdown
7. **Add WebSocket message size limits**
8. **Pre-allocate tier files** for better I/O performance
9. **Consider binary encoding** (MessagePack/Protobuf) for storage
10. **Add comprehensive error logging** instead of silent failures

### Low Priority (Enhancements)

11. **Add security headers** to HTTP responses
12. **Implement request timeouts**
13. **Add metrics/telemetry** for the monitoring tool itself
14. **Increase test coverage** (aim for >80%)
15. **Add benchmarks** for collector and storage operations

---

## 7. Conclusion

Kula-Szpiegula is a well-architected monitoring tool with a clean codebase. The tiered storage concept is innovative and appropriate for the use case. However, the security issues identified—particularly the mutex race condition and custom cryptography—should be addressed before production use.

The performance characteristics are reasonable for a tool collecting at 1-second intervals, but there are opportunities for optimization if higher frequency collection is needed in the future.

**Overall Rating:** 7/10
- Architecture: 8/10
- Code Quality: 7/10
- Security: 5/10 (due to identified vulnerabilities)
- Performance: 7/10
- Testing: 5/10

**Recommendation:** Address high-priority security issues before production deployment. The codebase is maintainable and would benefit from a security audit and increased test coverage.

---

*Report generated by Kimi K2.5 Agent - Security Expert & Code Reviewer*
