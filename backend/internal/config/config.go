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
	PreflightEnabled  bool
	PreflightCacheTTL time.Duration
	PreflightLockTTL  time.Duration
	PreflightWait     time.Duration
	Constants         Constants
}

func LoadServerConfig() ServerConfig {
	c := ServerConfig{
		Addr:              envOr("ADDR", ":8080"),
		ClickHouseDSN:     envOr("CLICKHOUSE_DSN", "clickhouse://default:@localhost:9000/sony_liv"),
		RedisAddr:         envOr("REDIS_ADDR", "localhost:6379"),
		PreflightEnabled:  envOr("PREFLIGHT_ENABLED", "true") == "true",
		PreflightCacheTTL: durationOr("PREFLIGHT_CACHE_TTL", 1*time.Minute),
		PreflightLockTTL:  durationOr("PREFLIGHT_LOCK_TTL", 30*time.Second),
		PreflightWait:     durationOr("PREFLIGHT_WAIT_TIMEOUT", 10*time.Second),
		Constants:         DefaultConstants(),
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
