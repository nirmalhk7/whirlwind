package dashboard

import "time"

// Keep missing times explicit rather than presenting the refresh time as history.
func savedInboxTimestamp(values ...string) string {
	for _, value := range values {
		if timestamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return timestamp.Local().Format("2006-01-02 15:04")
		}
	}
	return "time unknown"
}

func (d *Dashboard) recordTimestamp(kind, id string) string {
	for _, record := range d.snapshot[kind] {
		if record.ID == id {
			return savedInboxTimestamp(record.UpdatedAt)
		}
	}
	return "time unknown"
}
