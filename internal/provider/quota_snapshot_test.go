package provider

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"whirlwind/internal/model"
)

// Dropping a native window or manufacturing absent utilization must fail this test.
func TestQuotaSnapshotPreservesWindowsAndUnknowns(t *testing.T) {
	snapshot, err := ParseQuotaSnapshot(map[string]any{"rateLimitsByLimitId": map[string]any{"fast": map[string]any{"primary": map[string]any{"usedPercent": 83.0, "resetsAt": 1900000000.0}, "secondary": map[string]any{"resetsAt": 1900100000.0}}}}, "pool", "metadata", time.Unix(1800000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Pool != "pool" || len(snapshot.Windows) != 2 || snapshot.Windows[0].LimitID != "fast" || snapshot.Windows[0].UsedPercent == nil || *snapshot.Windows[0].UsedPercent != 83 || snapshot.Windows[1].UsedPercent != nil {
		t.Fatalf("lost native quota evidence: %+v", snapshot)
	}
}

// Native extension/applicability metadata must round-trip without being interpreted as a routing permission.
func TestQuotaSnapshotRoundTripRetainsNativeMetadata(t *testing.T) {
	fixture := json.RawMessage(`{"rateLimitsByLimitId":{"shared":{"limitName":"Native pool","primary":{"usedPercent":70,"resetsAt":1900000000,"windowDurationMins":300,"nativeExtension":{"opaque":"kept"}},"credits":{"hasCredits":false}}},"nativeMetadata":{"opaqueScope":["a","b"]}}`)
	snapshot, err := ParseQuotaSnapshot(fixture, "pool", "metadata", time.Unix(1800000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Raw           json.RawMessage `json:"raw_metadata"`
		Applicability string          `json:"applicability"`
	}
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	var original, retained any
	if err = json.Unmarshal(fixture, &original); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(decoded.Raw, &retained); err != nil {
		t.Fatalf("native metadata discarded: %s (%v)", encoded, err)
	}
	if !reflect.DeepEqual(original, retained) {
		t.Fatalf("native metadata changed: %s", decoded.Raw)
	}
	if decoded.Applicability != "unknown" {
		t.Fatalf("unverified applicability was inferred: %q", decoded.Applicability)
	}
}

func TestLegacyQuotaSnapshotReportsUnknownApplicability(t *testing.T) {
	snapshot := model.QuotaSnapshot{Pool: "legacy"}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err = json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	if value["applicability"] != "unknown" {
		t.Fatalf("missing applicability not explicit unknown: %s", encoded)
	}
}
