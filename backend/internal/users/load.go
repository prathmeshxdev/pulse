package users

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	"github.com/prathmeshxdev/pulse/internal/chclient"
	"github.com/prathmeshxdev/pulse/internal/models"
)

// BuildFromSessionSegments merges session segments into user islands + deltas.
func BuildFromSessionSegments(segs []models.Segment, version uint64) ([]models.UserSegment, []models.UserMinuteDelta) {
	userSegs := MergeIslands(segs, version)
	return userSegs, EmitClosedDeltas(userSegs)
}

// LoadClickHouse writes user_active_segments + user_minute_deltas with the same
// idempotent partition swap used for session tables.
func LoadClickHouse(ctx context.Context, conn driver.Conn, database string, segs []models.Segment, version uint64, rebuild bool) error {
	if !chclient.TableExists(ctx, conn, database, "user_active_segments") {
		return nil
	}
	userSegs, udeltas := BuildFromSessionSegments(segs, version)
	if !rebuild {
		if err := chclient.InsertUserSegments(ctx, conn, database+".user_active_segments", userSegs); err != nil {
			return err
		}
		return chclient.InsertUserDeltas(ctx, conn, database+".user_minute_deltas", udeltas)
	}
	segTimes := make([]time.Time, 0, len(userSegs))
	for _, s := range userSegs {
		segTimes = append(segTimes, s.SegmentStart)
	}
	deltaTimes := make([]time.Time, 0, len(udeltas))
	for _, d := range udeltas {
		deltaTimes = append(deltaTimes, d.Minute)
	}
	if err := chclient.StageAndReplace(ctx, conn, database, "user_active_segments",
		chclient.PartitionDays(segTimes...), func(stg string) error {
			return chclient.InsertUserSegments(ctx, conn, stg, userSegs)
		}); err != nil {
		return fmt.Errorf("user segments: %w", err)
	}
	if len(udeltas) == 0 {
		return nil
	}
	return chclient.StageAndReplace(ctx, conn, database, "user_minute_deltas",
		chclient.PartitionDays(deltaTimes...), func(stg string) error {
			return chclient.InsertUserDeltas(ctx, conn, stg, udeltas)
		})
}
