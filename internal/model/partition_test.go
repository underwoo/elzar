package model

import "testing"

func TestPartitionAccess(t *testing.T) {
	a := Association{Cluster: "c1", Account: "child", Lineage: "/root/parent/child/", QOS: []string{"normal", "burst"}}
	for _, tc := range []struct {
		name string
		p    Partition
		want bool
	}{
		{"empty all", Partition{}, true},
		{"account allowed", Partition{AllowedAccounts: []string{"child"}}, true},
		{"ancestor allowed", Partition{AllowedAccounts: []string{"parent"}}, true},
		{"account excluded", Partition{AllowedAccounts: []string{"other"}}, false},
		{"ancestor denied", Partition{DeniedAccounts: []string{"parent"}}, false},
		{"deny unrelated", Partition{DeniedAccounts: []string{"other"}}, true},
		{"allow overrides deny", Partition{AllowedAccounts: []string{"child"}, DeniedAccounts: []string{"child"}}, true},
		{"all overrides deny", Partition{AllowedAccounts: []string{"ALL"}, DeniedAccounts: []string{"child"}}, true},
		{"qos intersection", Partition{AllowedQOS: []string{"burst"}}, true},
		{"qos disjoint", Partition{AllowedQOS: []string{"special"}}, false},
		{"one qos denied", Partition{DeniedQOS: []string{"normal"}}, true},
		{"all qos denied", Partition{DeniedQOS: []string{"normal", "burst"}}, false},
		{"qos precedence", Partition{AllowedQOS: []string{"normal"}, DeniedQOS: []string{"normal", "burst"}}, true},
		{"exact names", Partition{AllowedAccounts: []string{"chi"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.p.Cluster = "c1"
			if got := tc.p.Allows(a); got != tc.want {
				t.Fatalf("Allows=%v want %v", got, tc.want)
			}
			tc.p.Cluster = "c2"
			if tc.p.Allows(a) {
				t.Fatal("cross-cluster access")
			}
		})
	}
	a.QOS = nil
	if !(Partition{Cluster: "c1", AllowedQOS: []string{"ALL"}, DeniedQOS: []string{"normal"}}).Allows(a) {
		t.Fatal("explicit ALL must be unrestricted")
	}
	if (Partition{Cluster: "c1", AllowedQOS: []string{"normal"}}).Allows(a) {
		t.Fatal("missing QOS grants gated access")
	}
}
