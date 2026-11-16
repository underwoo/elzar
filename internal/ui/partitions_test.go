package ui

import (
	"errors"
	"slurmtui/internal/model"
	"slurmtui/internal/slurm"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAccountPartitionDetail(t *testing.T) {
	a := detailAccountTestApp()
	a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	content := strings.Join(a.accountPartitionLines(), "\n")
	if !strings.Contains(content, "compute  State: UP") || !strings.Contains(content, "MaxTime (minutes): ∞") || strings.Contains(content, "wrong-cluster") {
		t.Fatal(content)
	}
	a.accounts.partitions[0].AllowedQOS = []string{"restricted"}
	if !strings.Contains(strings.Join(a.accountPartitionLines(), "\n"), "No partitions match") {
		t.Fatal("QOS restriction ignored")
	}
	a.accounts.partitionErrors = map[string]error{"c1": errors.New("offline")}
	if !strings.Contains(strings.Join(a.accountPartitionLines(), "\n"), "query failed") {
		t.Fatal("missing failure")
	}
	a.accounts.partitionErrors = nil
	a.accounts.partitionClusters = nil
	if !strings.Contains(strings.Join(a.accountPartitionLines(), "\n"), "unavailable") {
		t.Fatal("missing unavailable state")
	}
}
func TestPartitionRefreshFailureIsolation(t *testing.T) {
	a := detailAccountTestApp()
	assoc := a.accounts.associations
	a.acceptAccounts(accountsResult{username: "me", associations: slurm.AssociationsResult{Associations: assoc}, shares: slurm.SharesResult{Shares: a.accounts.shares}, partitions: slurm.PartitionsResult{Clusters: []string{"c1"}, Errors: []slurm.ClusterError{{Cluster: "c2", Err: errors.New("offline")}}, Partitions: []model.Partition{{Cluster: "c1", Name: "new"}}}})
	if !a.accounts.partitionClusters["c1"] || a.accounts.partitionClusters["c2"] || a.accounts.partitionErrors["c2"] == nil || len(a.accounts.partitions) != 1 {
		t.Fatal("failed cluster appeared current")
	}
	a.acceptAccounts(accountsResult{username: "me", assocErr: errors.New("associations unavailable"), shareErr: errors.New("shares unavailable")})
	if len(a.accounts.partitions) != 0 || len(a.accounts.partitionClusters) != 0 {
		t.Fatal("stale membership retained as current access")
	}
}
