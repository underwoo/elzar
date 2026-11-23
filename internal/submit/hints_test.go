package submit

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/underwoo/elzar/internal/model"
	"github.com/underwoo/elzar/internal/slurm"
)

type fixtureRunner struct {
	byCommand map[string][]byte
}

func (r *fixtureRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	data, ok := r.byCommand[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	return data, nil
}

func loadM3SiteFixtures(t *testing.T, site, partitionCluster string) ([]model.Association, []model.Partition, []model.QOS) {
	t.Helper()
	assocBytes, err := os.ReadFile("../../testdata/accounts/" + site + "_associations.json")
	if err != nil {
		t.Fatal(err)
	}
	qosBytes, err := os.ReadFile("../../testdata/accounts/" + site + "_qos.json")
	if err != nil {
		t.Fatal(err)
	}
	partBytes, err := os.ReadFile("../../testdata/partitions/" + site + ".json")
	if err != nil {
		t.Fatal(err)
	}

	r := &fixtureRunner{
		byCommand: map[string][]byte{
			"sacctmgr --json show assoc user=user-01":                    assocBytes,
			"sacctmgr --json show qos":                                   qosBytes,
			"scontrol -M " + partitionCluster + " show partition --json": partBytes,
		},
	}

	assocRes, err := slurm.ListAssociations(context.Background(), r, "user-01")
	if err != nil {
		t.Fatal(err)
	}
	qosRes, err := slurm.ListQOS(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	partRes := slurm.ListPartitions(context.Background(), r, []model.Association{{Cluster: partitionCluster}})
	if len(partRes.Errors) > 0 {
		t.Fatalf("partition errors: %v", partRes.Errors)
	}
	return assocRes.Associations, partRes.Partitions, qosRes.QOS
}

func TestHintsFromCommittedM3Fixtures(t *testing.T) {
	assocs, parts, qosList := loadM3SiteFixtures(t, "site-01", "cluster-01")

	t.Run("cluster-01 default account first and allowed partitions and QOS", func(t *testing.T) {
		hints := Hints("user-01", "cluster-01", assocs, parts, qosList)
		if hints.DefaultAccount != "account-07" {
			t.Fatalf("DefaultAccount = %q, want account-07", hints.DefaultAccount)
		}
		wantAccounts := []string{"account-07", "account-05", "account-06", "account-08", "account-09"}
		if !reflect.DeepEqual(hints.Accounts, wantAccounts) {
			t.Fatalf("Accounts = %v, want %v", hints.Accounts, wantAccounts)
		}

		// On cluster-01, account-07 has default QOS qos-01 and allowed QOS qos-01, qos-02, qos-05, qos-06, qos-07, qos-08.
		wantQOS07 := []string{"qos-01", "qos-02", "qos-05", "qos-06", "qos-07", "qos-08"}
		if got := hints.QOSFor("account-07", ""); !reflect.DeepEqual(got, wantQOS07) {
			t.Fatalf("QOSFor(account-07, '') = %v, want %v", got, wantQOS07)
		}

		// partition-04 in site-01.json allows only QOS qos-02, qos-03, qos-04.
		// Intersected with account-07's allowed QOS, only qos-02 is allowed.
		if got := hints.QOSFor("account-07", "partition-04"); !reflect.DeepEqual(got, []string{"qos-02"}) {
			t.Fatalf("QOSFor(account-07, partition-04) = %v, want [qos-02]", got)
		}

		// All 8 partitions in site-01.json are allowed for account-07 (since account-07 has qos-02).
		if got := hints.PartitionsForAccount("account-07"); len(got) != 8 {
			t.Fatalf("PartitionsForAccount(account-07) = %v (len %d), want 8 partitions", got, len(got))
		}
	})

	t.Run("cluster-05 has default account-08 first", func(t *testing.T) {
		hints := Hints("user-01", "cluster-05", assocs, parts, qosList)
		if hints.DefaultAccount != "account-08" {
			t.Fatalf("DefaultAccount = %q, want account-08", hints.DefaultAccount)
		}
		wantAccounts := []string{"account-08", "account-05", "account-06"}
		if !reflect.DeepEqual(hints.Accounts, wantAccounts) {
			t.Fatalf("Accounts = %v, want %v", hints.Accounts, wantAccounts)
		}
	})

	t.Run("site-03 account-gated partition-04", func(t *testing.T) {
		_, parts03, qos03 := loadM3SiteFixtures(t, "site-03", "cluster-08")
		// site-03.json has partition-04 gated to AllowedAccounts=["account-01"].
		synthAssocs := []model.Association{
			{Cluster: "cluster-08", Account: "account-02", User: "user-01", IsDefault: true, DefaultQOS: "qos-03", QOS: []string{"qos-03", "qos-04"}},
			{Cluster: "cluster-08", Account: "account-01", User: "user-01", IsDefault: false, DefaultQOS: "qos-03", QOS: []string{"qos-03", "qos-04"}},
		}
		hints := Hints("user-01", "cluster-08", synthAssocs, parts03, qos03)
		if !reflect.DeepEqual(hints.Accounts, []string{"account-02", "account-01"}) {
			t.Fatalf("Accounts = %v", hints.Accounts)
		}
		partsAcct1 := hints.PartitionsForAccount("account-01")
		partsAcct2 := hints.PartitionsForAccount("account-02")
		if !containsString(partsAcct1, "partition-04") {
			t.Fatalf("account-01 should have access to partition-04: %v", partsAcct1)
		}
		if containsString(partsAcct2, "partition-04") {
			t.Fatalf("account-02 should NOT have access to partition-04: %v", partsAcct2)
		}
	})

	t.Run("two-cluster fixture scopes pickers and warnings to effective or local cluster", func(t *testing.T) {
		assocs2 := []model.Association{
			{Cluster: "c1", Account: "acct-c1", User: "user-01", IsDefault: true, DefaultQOS: "qos-01", QOS: []string{"qos-01"}},
			{Cluster: "c2", Account: "acct-c2", User: "user-01", IsDefault: true, DefaultQOS: "qos-02", QOS: []string{"qos-02"}},
		}
		parts2 := []model.Partition{
			{Cluster: "c1", Name: "part-only-c1", State: []string{"UP"}},
			{Cluster: "c2", Name: "part-only-c2", State: []string{"UP"}},
		}
		qos2 := []model.QOS{
			{Name: "qos-01"},
			{Name: "qos-02"},
		}

		// With no cluster set, Hints defaults to the local cluster ("c1").
		hLocal := Hints("user-01", "", assocs2, parts2, qos2)
		if hLocal.Cluster != "c1" {
			t.Fatalf("local Cluster = %q, want c1", hLocal.Cluster)
		}
		if !reflect.DeepEqual(hLocal.Clusters, []string{"c1", "c2"}) {
			t.Fatalf("Clusters = %v, want [c1 c2]", hLocal.Clusters)
		}
		if !reflect.DeepEqual(hLocal.PartitionsForAccount(""), []string{"part-only-c1"}) {
			t.Fatalf("c1 PartitionsForAccount = %v, want [part-only-c1]", hLocal.PartitionsForAccount(""))
		}
		// part-only-c2 does not exist on c1 -> warning on c1.
		wC1 := Check(map[FieldID]string{FieldPartition: "part-only-c2"}, hLocal)
		if len(wC1) != 1 || wC1[0].Field != FieldPartition || !strings.Contains(wC1[0].Message, `"part-only-c2" is not in cluster partitions`) {
			t.Fatalf("c1 check for part-only-c2 = %+v", wC1)
		}

		// Switching FieldClusters to "c2" scopes Check (and Hints("user-01", "c2", ...)) to c2.
		hC2 := Hints("user-01", "c2", assocs2, parts2, qos2)
		if !reflect.DeepEqual(hC2.PartitionsForAccount(""), []string{"part-only-c2"}) {
			t.Fatalf("c2 PartitionsForAccount = %v, want [part-only-c2]", hC2.PartitionsForAccount(""))
		}
		if w := Check(map[FieldID]string{FieldClusters: "c2", FieldPartition: "part-only-c2"}, hLocal); len(w) != 0 {
			t.Fatalf("c2 check for part-only-c2 = %+v, want 0 warnings", w)
		}
		wC2 := Check(map[FieldID]string{FieldClusters: "c2", FieldPartition: "part-only-c1"}, hLocal)
		if len(wC2) != 1 || wC2[0].Field != FieldPartition || !strings.Contains(wC2[0].Message, `"part-only-c1" is not in cluster partitions`) {
			t.Fatalf("c2 check for part-only-c1 = %+v", wC2)
		}
	})
}

func TestCheckWarningsWithM3Fixtures(t *testing.T) {
	assocs, parts, qosList := loadM3SiteFixtures(t, "site-01", "cluster-01")
	// Add qos-03 to account-07 on cluster-01 so we can also test qos-03's 60-minute MaxWall from site-01_qos.json.
	for i := range assocs {
		if assocs[i].Cluster == "cluster-01" && assocs[i].Account == "account-07" {
			assocs[i].QOS = append(assocs[i].QOS, "qos-03")
		}
	}
	hints := Hints("user-01", "cluster-01", assocs, parts, qosList)

	t.Run("valid values produce zero warnings", func(t *testing.T) {
		warnings := Check(map[FieldID]string{
			FieldAccount:   "account-07",
			FieldPartition: "partition-01",
			FieldQOS:       "qos-03",
			FieldTime:      "01:00:00", // 60 min <= qos-03 MaxWall (60) and partition-01 MaxTime (960)
			FieldNodes:     "1",        // 1 <= partition-01 MaxNodes (1)
		}, hints)
		if len(warnings) != 0 {
			t.Fatalf("expected 0 warnings, got %+v", warnings)
		}
	})

	t.Run("account not in associations", func(t *testing.T) {
		warnings := Check(map[FieldID]string{
			FieldAccount: "unknown-acct",
		}, hints)
		if len(warnings) != 1 || warnings[0].Field != FieldAccount || !strings.Contains(warnings[0].Message, "not in your associations") {
			t.Fatalf("warnings = %+v", warnings)
		}
	})

	t.Run("QOS not allowed for account", func(t *testing.T) {
		// account-09 on cluster-01 allows only qos-02, qos-06, qos-07, qos-08 (not qos-01).
		warnings := Check(map[FieldID]string{
			FieldAccount: "account-09",
			FieldQOS:     "qos-01",
		}, hints)
		if len(warnings) != 1 || warnings[0].Field != FieldQOS || !strings.Contains(warnings[0].Message, `QOS "qos-01" is not allowed for account "account-09"`) {
			t.Fatalf("warnings = %+v", warnings)
		}
	})

	t.Run("QOS not allowed on partition", func(t *testing.T) {
		// partition-04 allows only qos-02, qos-03, qos-04 (not qos-01).
		warnings := Check(map[FieldID]string{
			FieldAccount:   "account-07",
			FieldPartition: "partition-04",
			FieldQOS:       "qos-01",
		}, hints)
		if len(warnings) != 1 || warnings[0].Field != FieldQOS || !strings.Contains(warnings[0].Message, `QOS "qos-01" is not allowed on partition "partition-04"`) {
			t.Fatalf("warnings = %+v", warnings)
		}
	})

	t.Run("time exceeds QOS MaxWall and partition MaxTime", func(t *testing.T) {
		// qos-03 in site-01_qos.json has MaxWall = 60 minutes.
		// partition-01 in site-01.json has MaxTime = 960 minutes (16 hours).
		warnings := Check(map[FieldID]string{
			FieldAccount:   "account-07",
			FieldPartition: "partition-01",
			FieldQOS:       "qos-03",
			FieldTime:      "02:00:00", // 120 min > 60 min QOS MaxWall
		}, hints)
		if len(warnings) != 1 || warnings[0].Field != FieldTime || !strings.Contains(warnings[0].Message, `exceeds QOS "qos-03" MaxWall (60 min)`) {
			t.Fatalf("warnings = %+v", warnings)
		}

		// 1-00:00:00 (1440 min) exceeds both qos-03 MaxWall (60 min) and partition-01 MaxTime (960 min).
		warnings = Check(map[FieldID]string{
			FieldAccount:   "account-07",
			FieldPartition: "partition-01",
			FieldQOS:       "qos-03",
			FieldTime:      "1-00:00:00",
		}, hints)
		if len(warnings) != 2 {
			t.Fatalf("expected 2 time warnings, got %+v", warnings)
		}
	})

	t.Run("nodes exceeds partition MaxNodes and QOS MaxTRESPerJob node limit", func(t *testing.T) {
		// partition-01 in site-01.json has MaxNodes = 1.
		warnings := Check(map[FieldID]string{
			FieldAccount:   "account-07",
			FieldPartition: "partition-01",
			FieldNodes:     "2-4",
		}, hints)
		if len(warnings) != 1 || warnings[0].Field != FieldNodes || !strings.Contains(warnings[0].Message, `exceeds partition "partition-01" MaxNodes (1)`) {
			t.Fatalf("warnings = %+v", warnings)
		}

		// site-03_qos.json has qos-03 with MaxTRESPerJob node = 75.
		_, parts03, qos03 := loadM3SiteFixtures(t, "site-03", "cluster-08")
		hints03 := Hints("user-01", "cluster-08", []model.Association{
			{Cluster: "cluster-08", Account: "account-01", User: "user-01", IsDefault: true, DefaultQOS: "qos-03", QOS: []string{"qos-03"}},
		}, parts03, qos03)
		warnings = Check(map[FieldID]string{
			FieldAccount:   "account-01",
			FieldPartition: "partition-02", // infinite MaxNodes in site-03.json
			FieldQOS:       "qos-03",
			FieldNodes:     "100",
		}, hints03)
		if len(warnings) != 1 || warnings[0].Field != FieldNodes || !strings.Contains(warnings[0].Message, `exceeds QOS "qos-03" MaxTRESPerJob node limit (75)`) {
			t.Fatalf("warnings = %+v", warnings)
		}
	})
}

func TestParseTimeMinutes(t *testing.T) {
	tests := []struct {
		in       string
		wantMin  int64
		wantInf  bool
		wantGood bool
	}{
		{"30", 30, false, true},
		{"30:00", 30, false, true},
		{"30:01", 31, false, true}, // rounds up seconds like Slurm time_str2mins
		{"01:30:00", 90, false, true},
		{"1-00", 1440, false, true},
		{"2-12:30:00", 3630, false, true},
		{"UNLIMITED", 0, true, true},
		{"infinite", 0, true, true},
		{"-1", 0, true, true},
		{"0", 0, true, true},
		{"", 0, false, false},
		{"invalid", 0, false, false},
		{"1:2:3:4", 0, false, false},
	}
	for _, tt := range tests {
		mins, inf, ok := ParseTimeMinutes(tt.in)
		if ok != tt.wantGood || mins != tt.wantMin || inf != tt.wantInf {
			t.Errorf("ParseTimeMinutes(%q) = (%d, %v, %v), want (%d, %v, %v)", tt.in, mins, inf, ok, tt.wantMin, tt.wantInf, tt.wantGood)
		}
	}
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}
