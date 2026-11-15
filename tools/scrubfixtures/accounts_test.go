package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLabelledAccountDocuments(t *testing.T) {
	data := []byte("sshare --json -l -U\n{\n\"shares\": {\n\"shares\": []\n}\n}\nsacctmgr --json show assoc user=private\n{\"associations\":[]}\nsacctmgr --json show qos\n{\"qos\":[]}\nscontrol show partition --json\n{\"partitions\":[]}")
	docs, err := labelledDocuments(data)
	if err != nil || len(docs) != 3 {
		t.Fatalf("docs=%v err=%v", docs, err)
	}
	for _, d := range docs {
		if !json.Valid(d.data) || bytes.Contains(d.data, []byte("private")) {
			t.Fatalf("bad document %s", d.data)
		}
	}
	for _, data := range []string{"nothing", "sshare --json\n{", "sacctmgr --json show assoc user=private\ninvalid"} {
		if _, err := labelledDocuments([]byte(data)); err == nil {
			t.Fatalf("accepted %q", data)
		}
	}
}

func TestAccountIdentityConsistency(t *testing.T) {
	s := newScrubber()
	raw := `{"shares":{"shares":[{"id":123,"cluster":"secret-cluster","name":"secret-account","parent":"secret-parent","type":["ASSOCIATION"]},{"id":124,"cluster":"secret-cluster","name":"secret-user","parent":"secret-account","type":["USER"]}]},"associations":[{"id":124,"account":"secret-account","parent_account":"secret-parent","user":"secret-user","cluster":"secret-cluster","lineage":"/secret-parent/secret-account/","qos":["secret-qos"],"default":{"qos":"secret-qos"}}],"qos":[{"name":"secret-qos","description":"secret-description","limits":{"max":{"tres":{"per":{"job":[{"name":"gpu","type":"gres","id":1001,"count":4}]}}}}}]}`
	value, err := decodeSingleJSON([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	cleaned := s.cleanAccountRoot(value).(map[string]any)
	out, err := json.Marshal(cleaned)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "secret") {
		t.Fatalf("identity leaked: %s", out)
	}
	rows := cleaned["shares"].(map[string]any)["shares"].([]any)
	account := rows[0].(map[string]any)
	user := rows[1].(map[string]any)
	assoc := cleaned["associations"].([]any)[0].(map[string]any)
	if account["name"] != assoc["account"] || user["name"] != assoc["user"] || user["parent"] != account["name"] || user["id"] != assoc["id"] {
		t.Fatal("inconsistent identities")
	}
	if assoc["lineage"] != "/"+account["parent"].(string)+"/"+account["name"].(string)+"/" {
		t.Fatal("lineage mismatch")
	}
	if !bytes.Contains(out, []byte(`"name":"gpu"`)) || !bytes.Contains(out, []byte(`"id":1001`)) {
		t.Fatal("TRES identity corrupted")
	}
	qos := cleaned["qos"].([]any)[0].(map[string]any)
	if assoc["qos"].([]any)[0] != qos["name"] || assoc["default"].(map[string]any)["qos"] != qos["name"] {
		t.Fatal("QOS mismatch")
	}
}

func TestScrubMultiClusterAndDirectory(t *testing.T) {
	s := newScrubber()
	data := []byte("CLUSTER: private-cluster\n{\"shares\":{\"shares\":[{\"cluster\":\"private-cluster\",\"name\":\"private-account\",\"type\":[\"ASSOCIATION\"]}]}}\nCLUSTER: second\n{\"shares\":{\"shares\":[]}}")
	got, err := scrubClusterDocuments(data, s)
	if err != nil || bytes.Contains(got, []byte("private")) || !bytes.Contains(got, []byte("CLUSTER: cluster-01")) || !bytes.Contains(got, []byte(`"cluster": "cluster-01"`)) {
		t.Fatalf("%s %v", got, err)
	}
	in := t.TempDir()
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(in, "sshare-all.out"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(in, "site.out"), []byte("sacctmgr --json show assoc user=private\n{\"associations\":[{\"cluster\":\"private-cluster\",\"account\":\"private-account\"}]}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := scrubDirectory(in, out); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(out, "site-01_associations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := scrubDirectory(in, out); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(out, "site-01_associations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || bytes.Contains(first, []byte("private")) {
		t.Fatal("unstable or unsanitized output")
	}
}
