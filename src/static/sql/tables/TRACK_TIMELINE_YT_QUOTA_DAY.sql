-- Local estimate of YouTube Data API units spent today. Google does not
-- return remaining quota on API responses; the Cloud Console is authoritative.
-- This table only powers the admin Dead Videos page's usage meter.
CREATE TABLE IF NOT EXISTS TRACK_TIMELINE_YT_QUOTA_DAY(
    USAGE_DATE DATE NOT NULL,
    UNITS_USED INT NOT NULL DEFAULT 0,
    PRIMARY KEY(USAGE_DATE)
);
