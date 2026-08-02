package users

import (
	"hash/fnv"
	"sort"
	"time"

	"github.com/prathmeshxdev/pulse/internal/models"
)

// MergeIslands merges session_active_segments per user_id into non-overlapping
// islands, then applies the same sweep-line delta model at user grain.
// Overlapping sessions for one user collapse to a single concurrent viewer.
func MergeIslands(segs []models.Segment, version uint64) []models.UserSegment {
	byUser := map[string][]models.Segment{}
	for _, s := range segs {
		if s.UserID == "" {
			continue
		}
		byUser[s.UserID] = append(byUser[s.UserID], s)
	}
	out := make([]models.UserSegment, 0)
	for uid, list := range byUser {
		sort.Slice(list, func(i, j int) bool {
			if list[i].SegmentStart.Equal(list[j].SegmentStart) {
				return list[i].SegmentEnd.Before(list[j].SegmentEnd)
			}
			return list[i].SegmentStart.Before(list[j].SegmentStart)
		})
		var cur *island
		for _, s := range list {
			if cur == nil || !s.SegmentStart.Before(cur.end) {
				if cur != nil {
					out = append(out, cur.finish(uid, version))
				}
				cur = &island{
					start:   s.SegmentStart,
					end:     s.SegmentEnd,
					dims:    s,
					hasOpen: s.CloseReason == "",
				}
				continue
			}
			if s.SegmentEnd.After(cur.end) {
				cur.end = s.SegmentEnd
			}
			if s.CloseReason == "" {
				cur.hasOpen = true
			}
		}
		if cur != nil {
			out = append(out, cur.finish(uid, version))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UserID != out[j].UserID {
			return out[i].UserID < out[j].UserID
		}
		return out[i].SegmentStart.Before(out[j].SegmentStart)
	})
	return out
}

type island struct {
	start, end time.Time
	dims         models.Segment
	hasOpen      bool
}

func (i *island) finish(userID string, version uint64) models.UserSegment {
	closeReason := models.CloseReasonSessionEnd
	if i.hasOpen {
		closeReason = ""
	}
	return models.UserSegment{
		UserSegmentID:    UserSegmentID(userID, i.start),
		UserID:           userID,
		ContentID:        i.dims.ContentID,
		Platform:         i.dims.Platform,
		Country:          i.dims.Country,
		AppVersion:       i.dims.AppVersion,
		AudioLanguage:    i.dims.AudioLanguage,
		SubtitleLanguage: i.dims.SubtitleLanguage,
		PlayerVersion:    i.dims.PlayerVersion,
		VideoType:        i.dims.VideoType,
		Category:         i.dims.Category,
		SegmentStart:     i.start,
		SegmentEnd:       i.end,
		CloseReason:      closeReason,
		Version:          version,
		Properties:       i.dims.Properties,
	}
}

// UserSegmentID is deterministic: FNV-1a over (user_id, start_ms).
func UserSegmentID(userID string, start time.Time) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(userID))
	_, _ = h.Write([]byte{0})
	ms := start.UTC().UnixMilli()
	var buf [8]byte
	for i := 0; i < 8; i++ {
		buf[i] = byte(ms >> (8 * i))
	}
	_, _ = h.Write(buf[:])
	return h.Sum64()
}
