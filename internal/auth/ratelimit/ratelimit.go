package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

// Thresholds (matching PyKYCH).
const (
	maxFailuresPerUserIP = 5
	maxFailuresPerIP     = 20
	windowSeconds        = 60
	lockoutThreshold     = 10
	lockoutDuration      = 15 * 60 // 900s
	// cleanupInterval is how often the background sweeper runs. Without it,
	// failures/lockouts for one-shot (username, ip) pairs are only ever
	// reaped when that exact key is accessed again, so an attacker spraying
	// random keys could grow the maps unbounded (memory DoS).
	cleanupInterval = 5 * time.Minute
)

type record struct {
	ts    float64
	count int
}

// Limiter is an in-memory rate limiter for login attempts.
// It is safe for concurrent use.
type Limiter struct {
	mu       sync.Mutex
	failures map[string][]record
	lockouts map[string]float64
}

// New creates a new in-memory Limiter and starts a background sweeper that
// reaps expired failure records and lockouts. The sweeper runs for the
// lifetime of the process; without it, entries for (username, ip) pairs that
// are never revisited would accumulate indefinitely.
func New() *Limiter {
	l := &Limiter{
		failures: make(map[string][]record),
		lockouts: make(map[string]float64),
	}
	go l.cleanupLoop()
	return l
}

func userKey(username, ip string) string    { return "user:" + username + ":" + ip }
func ipKey(ip string) string                { return "ip:" + ip }
func lockoutKey(username, ip string) string { return "lockout:" + username + ":" + ip }

// cleanOld removes records older than the window and returns the sum of counts.
func cleanOld(recs []record, now float64) ([]record, int) {
	cutoff := now - windowSeconds
	kept := recs[:0]
	total := 0
	for _, r := range recs {
		if r.ts > cutoff {
			kept = append(kept, r)
			total += r.count
		}
	}
	return kept, total
}

// CheckAllowed reports whether a login attempt is allowed for (username, ip).
// Returns (true, "") if allowed, (false, reason) if rate-limited/locked.
func (l *Limiter) CheckAllowed(username, ip string) (bool, string) {
	now := float64(time.Now().Unix())
	l.mu.Lock()
	defer l.mu.Unlock()

	// 1. Lockout check.
	if lockEnd, ok := l.lockouts[lockoutKey(username, ip)]; ok && now < lockEnd {
		remaining := int(lockEnd - now)
		mins := remaining / 60
		secs := remaining % 60
		return false, fmt.Sprintf("由于多次登录失败，该账号已暂时锁定。请在 %d 分 %d 秒后重试。", mins, secs)
	}

	uk := userKey(username, ip)
	ik := ipKey(ip)

	// 2. Global IP limit.
	if recs, ok := l.failures[ik]; ok {
		kept, total := cleanOld(recs, now)
		l.failures[ik] = kept
		if total >= maxFailuresPerIP {
			return false, "来自该 IP 的登录尝试过于频繁，请稍后重试。"
		}
	}

	// 3. User+IP limit.
	if recs, ok := l.failures[uk]; ok {
		kept, total := cleanOld(recs, now)
		l.failures[uk] = kept
		if total >= maxFailuresPerUserIP {
			return false, "该账号登录尝试过于频繁，请稍后重试。"
		}
	}

	return true, ""
}

// RecordFailure logs a failed attempt and may trigger a lockout.
func (l *Limiter) RecordFailure(username, ip string) {
	now := float64(time.Now().Unix())
	l.mu.Lock()
	defer l.mu.Unlock()

	uk := userKey(username, ip)
	ik := ipKey(ip)

	l.failures[uk] = cleanOldAppend(l.failures[uk], now)
	l.failures[ik] = cleanOldAppend(l.failures[ik], now)

	// Lockout if cumulative user+IP failures hit threshold.
	_, total := cleanOld(l.failures[uk], now)
	if total >= lockoutThreshold {
		l.lockouts[lockoutKey(username, ip)] = now + lockoutDuration
	}
}

// Reset clears failure/lockout records for (username, ip) on success.
// Note: the global ip counter is intentionally NOT reset.
func (l *Limiter) Reset(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, userKey(username, ip))
	delete(l.lockouts, lockoutKey(username, ip))
}

func cleanOldAppend(recs []record, now float64) []record {
	recs, _ = cleanOld(recs, now)
	return append(recs, record{ts: now, count: 1})
}

// cleanupLoop periodically drops expired entries so maps don't grow unbounded
// under a spray of distinct (username, ip) keys.
func (l *Limiter) cleanupLoop() {
	t := time.NewTicker(cleanupInterval)
	defer t.Stop()
	for range t.C {
		l.cleanup()
	}
}

// cleanup removes all expired failure records (and keys that become empty as a
// result) plus expired lockouts. Called under l.mu.
func (l *Limiter) cleanup() {
	now := float64(time.Now().Unix())
	l.mu.Lock()
	defer l.mu.Unlock()

	for k, recs := range l.failures {
		kept, _ := cleanOld(recs, now)
		if len(kept) == 0 {
			delete(l.failures, k)
		} else {
			l.failures[k] = kept
		}
	}
	for k, lockEnd := range l.lockouts {
		if now >= lockEnd {
			delete(l.lockouts, k)
		}
	}
}
