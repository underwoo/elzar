package ui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"slurmtui/internal/slurm"
)

type pluginFixtureRunner []byte

func (r pluginFixtureRunner) Run(context.Context, string, ...string) ([]byte, error) { return r, nil }

func TestPluginFieldsFromSlurmFixtures(t *testing.T) {
	for _, filename := range []string{"squeue-02.json", "sacct_recent.json", "sacct_failed.json"} {
		t.Run(filename, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "testdata", filename))
			if err != nil {
				t.Fatal(err)
			}
			var document struct{ Jobs []map[string]json.RawMessage }
			if err = json.Unmarshal(data, &document); err != nil {
				t.Fatal(err)
			}
			runner := pluginFixtureRunner(data)
			var result slurm.ListResult
			if filename == "squeue-02.json" {
				result, err = slurm.ListJobs(context.Background(), runner, slurm.ScopeMe)
			} else {
				result, err = slurm.ListRecent(context.Background(), runner, slurm.ScopeMe, time.Hour, time.Now)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Jobs) == 0 {
				t.Fatal("fixture has no terminal jobs")
			}
			job := result.Jobs[0]
			fields := jobPluginFields(job)
			for _, name := range []string{"JobID", "Name", "User", "Account", "Partition", "QOS", "State", "Cluster", "NodeList", "FirstNode", "WorkDir", "StdOut", "Comment"} {
				if _, ok := fields[name]; !ok {
					t.Errorf("fixture field %s absent", name)
				}
			}
			var expanded string
			if err = json.Unmarshal(document.Jobs[0]["stdout_expanded"], &expanded); err != nil {
				t.Fatal(err)
			}
			if fields["StdOut"] != expanded {
				t.Fatalf("StdOut=%q want expanded %q", fields["StdOut"], expanded)
			}
			if filename == "squeue-02.json" && fields["Comment"] != "comment-01" {
				t.Fatalf("Comment=%q", fields["Comment"])
			}
			if filename == "sacct_failed.json" {
				if _, ok := fields["StdErr"]; ok {
					t.Fatal("empty stderr should be unavailable")
				}
			}
		})
	}
}
