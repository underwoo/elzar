package slurm

import (
	"context"
	"encoding/json"
	"fmt"
	"slurmtui/internal/model"
	"sort"
	"strings"
)

type PartitionsResult struct {
	Partitions []model.Partition
	Clusters   []string
	Errors     []ClusterError
	Warnings   []string
}

// ListPartitions makes one explicitly scoped call for each association cluster.
// An unavailable cluster never discards successful results from another cluster.
func ListPartitions(ctx context.Context, runner Runner, associations []model.Association) PartitionsResult {
	result := PartitionsResult{}
	seen := map[string]bool{}
	for _, a := range associations {
		if a.Cluster != "" {
			seen[a.Cluster] = true
		}
	}
	clusters := make([]string, 0, len(seen))
	for c := range seen {
		clusters = append(clusters, c)
	}
	sort.Strings(clusters)
	for _, cluster := range clusters {
		rows, warnings, err := listClusterPartitions(ctx, runner, cluster)
		if err != nil {
			result.Errors = append(result.Errors, ClusterError{Cluster: cluster, Err: err})
			continue
		}
		result.Clusters = append(result.Clusters, cluster)
		result.Partitions = append(result.Partitions, rows...)
		for _, w := range warnings {
			result.Warnings = append(result.Warnings, "cluster "+cluster+": "+w)
		}
	}
	return result
}
func listClusterPartitions(ctx context.Context, runner Runner, cluster string) ([]model.Partition, []string, error) {
	data, err := runner.Run(ctx, "scontrol", "-M", cluster, "show", "partition", "--json")
	if err != nil {
		return nil, nil, fmt.Errorf("run scontrol partition: %w", err)
	}
	type access struct {
		Allowed string `json:"allowed"`
		Deny    string `json:"deny"`
	}
	var response struct {
		accountEnvelope
		Partitions []struct {
			Name     string `json:"name"`
			Accounts access `json:"accounts"`
			QOS      access `json:"qos"`
			Maximums struct {
				Time  model.NoValNumber `json:"time"`
				Nodes model.NoValNumber `json:"nodes"`
			} `json:"maximums"`
			Defaults struct {
				Time model.NoValNumber `json:"time"`
			} `json:"defaults"`
			Partition struct {
				State []string `json:"state"`
			} `json:"partition"`
		} `json:"partitions"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, nil, fmt.Errorf("decode partitions JSON: %w", err)
	}
	if err := response.validate(); err != nil {
		return nil, nil, err
	}
	rows := []model.Partition{}
	for _, r := range response.Partitions {
		rows = append(rows, model.Partition{Cluster: cluster, Name: r.Name, AllowedAccounts: partitionList(r.Accounts.Allowed), DeniedAccounts: partitionList(r.Accounts.Deny), AllowedQOS: partitionList(r.QOS.Allowed), DeniedQOS: partitionList(r.QOS.Deny), MaxTime: r.Maximums.Time, MaxNodes: r.Maximums.Nodes, DefaultTime: r.Defaults.Time, State: r.Partition.State})
	}
	return rows, response.warnings(), nil
}
func partitionList(value string) []string {
	var out []string
	for _, s := range strings.Split(value, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}
