package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"
)

// Constants are the frozen semantic knobs from FINAL_PLAN §1.6.
type Constants struct {
	Database              string
	HeartbeatGraceSec     int
	Timezone              string
	MinuteAttribution     string
	AvgDenominator        string
	SessionGrain          string
	MaxSegmentSpanHours   int
	PauseCountsAsActive   bool
	BufferingCountsActive bool
}

func DefaultConstants() Constants {
	return Constants{
		Database:              "sony_liv",
		HeartbeatGraceSec:     90,
		Timezone:              "UTC",
		MinuteAttribution:     "any_overlap",
		AvgDenominator:        "all_clock_minutes",
		SessionGrain:          "video_session_id",
		MaxSegmentSpanHours:   72,
		PauseCountsAsActive:   false,
		BufferingCountsActive: true,
	}
}

func (c Constants) HeartbeatGrace() time.Duration {
	return time.Duration(c.HeartbeatGraceSec) * time.Second
}

func (c Constants) MaxSegmentSpan() time.Duration {
	return time.Duration(c.MaxSegmentSpanHours) * time.Hour
}

// DefaultCountUnit maps SESSION_GRAIN config to the chart/bench counting unit.
func (c Constants) DefaultCountUnit() string {
	if strings.EqualFold(c.SessionGrain, "user_id") {
		return "user"
	}
	return "session"
}

// LoadConstantsFromEnvFile reads clickhouse/scripts/config.env style KEY=VALUE lines.
func LoadConstantsFromEnvFile(path string) (Constants, error) {
	c := DefaultConstants()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "DATABASE":
			c.Database = v
		case "HEARTBEAT_GRACE_SEC":
			if n, err := strconv.Atoi(v); err == nil {
				c.HeartbeatGraceSec = n
			}
		case "TIMEZONE":
			c.Timezone = v
		case "MINUTE_ATTRIBUTION":
			c.MinuteAttribution = v
		case "AVG_DENOMINATOR":
			c.AvgDenominator = v
		case "SESSION_GRAIN":
			c.SessionGrain = v
		case "MAX_SEGMENT_SPAN_HOURS":
			if n, err := strconv.Atoi(v); err == nil {
				c.MaxSegmentSpanHours = n
			}
		case "PAUSE_COUNTS_AS_ACTIVE":
			c.PauseCountsAsActive = strings.EqualFold(v, "true")
		case "BUFFERING_COUNTS_AS_ACTIVE":
			c.BufferingCountsActive = strings.EqualFold(v, "true")
		}
	}
	return c, sc.Err()
}

// ServerConfig holds runtime knobs for the HTTP API.
type ServerConfig struct {
	Addr              string
	ClickHouseDSN     string
	RedisAddr         string
	RedisPassword     string
	PreflightEnabled  bool
	PreflightCacheTTL time.Duration
	PreflightLockTTL  time.Duration
	PreflightWait     time.Duration
	// LiveEnabled turns on the Redis-backed exact live-concurrency path
	// (internal/livestate). Independent of PreflightEnabled — Redis is used
	// for two different things (query cache vs. live session state).
	LiveEnabled bool
	// LiveTTL bounds how late an event may arrive and still be folded into its
	// session via the fast Redis path. FIXED (non-refreshing) — set once when
	// a session's key is first created, never extended by later writes. Zero
	// defaults to Constants.MaxSegmentSpanHours (72h in the locked config),
	// reusing the same bound the R9 query lookback already asserts no segment
	// exceeds (measured max in the training data: 43.64h, ~28h of margin).
	// Sessions silent longer than this are reconcile-or-drop (cmd/reconcile).
	LiveTTL   time.Duration
	Constants Constants
}

func LoadServerConfig() ServerConfig {
	c := ServerConfig{
		Addr:              envOr("ADDR", ":8080"),
		ClickHouseDSN:     envOr("CLICKHOUSE_DSN", "clickhouse://default:@localhost:9000/sony_liv"),
		RedisAddr:         resolveRedisAddr(),
		RedisPassword:     os.Getenv("REDIS_PASSWORD"),
		PreflightEnabled:  envOr("PREFLIGHT_ENABLED", "true") == "true",
		PreflightCacheTTL: durationOr("PREFLIGHT_CACHE_TTL", 1*time.Minute),
		PreflightLockTTL:  durationOr("PREFLIGHT_LOCK_TTL", 30*time.Second),
		PreflightWait:     durationOr("PREFLIGHT_WAIT_TIMEOUT", 10*time.Second),
		LiveEnabled:       envOr("LIVE_ENABLED", "true") == "true",
		// 0 = fall back to Constants.MaxSegmentSpanHours inside livestate.New;
		// LIVE_TTL lets an operator override that explicitly if ever needed.
		LiveTTL:   durationOr("LIVE_TTL", 0),
		Constants: DefaultConstants(),
	}
	if path := os.Getenv("CONFIG_ENV"); path != "" {
		if loaded, err := LoadConstantsFromEnvFile(path); err == nil {
			c.Constants = loaded
		}
	}
	return c
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// resolveRedisAddr accepts either REDIS_ADDR ("host:port") or the separate
// REDIS_HOST/REDIS_PORT pair (the shape managed Redis providers, e.g. Redis
// Cloud, typically hand out alongside REDIS_PASSWORD).
func resolveRedisAddr() string {
	if addr := os.Getenv("REDIS_ADDR"); addr != "" {
		return addr
	}
	if host := os.Getenv("REDIS_HOST"); host != "" {
		port := envOr("REDIS_PORT", "6379")
		return host + ":" + port
	}
	return "localhost:6379"
}

func durationOr(k string, def time.Duration) time.Duration {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
