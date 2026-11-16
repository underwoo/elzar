package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPartitionScrubbing(t *testing.T) {
	raw := `{"partitions":[{"name":"private-partition","nodes":{"configured":"private-node"},"groups":{"allowed":"private-group"},"accounts":{"allowed":"private-account,ALL","deny":"private-denied"},"qos":{"allowed":"private-qos","deny":"","assigned":"private-qos"},"defaults":{"time":{"set":true,"number":5},"job":"private-job"},"maximums":{"time":{"infinite":true},"nodes":{"set":true,"number":4}},"partition":{"state":["UP"]}}]}`
	value, err := decodeSingleJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	clean := newScrubber().cleanAccountRoot(value)
	data, err := json.Marshal(clean)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "private-") {
		t.Fatalf("identity leaked: %s", data)
	}
	row := clean.(map[string]any)["partitions"].([]any)[0].(map[string]any)
	qos := row["qos"].(map[string]any)
	if qos["allowed"] != qos["assigned"] || qos["deny"] != "" {
		t.Fatal(qos)
	}
	if row["accounts"].(map[string]any)["allowed"] != "account-01,ALL" {
		t.Fatal(row)
	}
	if !strings.Contains(string(data), `"infinite":true`) || !strings.Contains(string(data), `"UP"`) {
		t.Fatal("lost semantics")
	}
}
