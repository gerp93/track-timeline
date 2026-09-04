package database

import (
	"errors"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultYouTubeDailyQuota is Google's default project quota for the YouTube
// Data API v3. Override with TRACK_TIMELINE_YT_DAILY_QUOTA when the Cloud
// Console project has a different limit.
const DefaultYouTubeDailyQuota = 10000

// YouTubeDailyQuotaLimit returns the configured daily unit budget.
func YouTubeDailyQuotaLimit() int {
	raw := strings.TrimSpace(os.Getenv("TRACK_TIMELINE_YT_DAILY_QUOTA"))
	if raw == "" {
		return DefaultYouTubeDailyQuota
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultYouTubeDailyQuota
	}
	return n
}

// AddYouTubeQuotaUsage increments today's estimated unit spend.
func AddYouTubeQuotaUsage(units int) error {
	if units <= 0 {
		return nil
	}
	day := time.Now().UTC().Format("2006-01-02")
	return execute(`
		INSERT INTO TRACK_TIMELINE_YT_QUOTA_DAY(USAGE_DATE, UNITS_USED)
		VALUES (?, ?)
		ON DUPLICATE KEY UPDATE UNITS_USED = UNITS_USED + VALUES(UNITS_USED)
	`, day, units)
}

// GetYouTubeQuotaUsedToday returns estimated units spent today (UTC).
func GetYouTubeQuotaUsedToday() (int, error) {
	day := time.Now().UTC().Format("2006-01-02")
	rows, err := query(
		"SELECT UNITS_USED FROM TRACK_TIMELINE_YT_QUOTA_DAY WHERE USAGE_DATE = ?",
		day,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	if !rows.Next() {
		return 0, nil
	}
	var used int
	if err := rows.Scan(&used); err != nil {
		log.Println(err)
		return 0, errors.New("failed to scan row in query results")
	}
	return used, nil
}
