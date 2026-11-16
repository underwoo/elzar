package slurm

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/underwoo/elzar/internal/model"
)

func TestPartitionFixtures(t *testing.T) {
	for _, tc := range []struct {
		file  string
		count int
	}{{"site-01.json", 8}, {"site-02.json", 11}, {"site-03.json", 7}} {
		t.Run(tc.file, func(t *testing.T) {
			data, err := os.ReadFile("../../testdata/partitions/" + tc.file)
			if err != nil {
				t.Fatal(err)
			}
			r := newFakeRunner()
			r.responses["scontrol -M target show partition --json"] = data
			got := ListPartitions(context.Background(), r, []model.Association{{Cluster: "target"}, {Cluster: "target"}})
			if len(got.Errors) > 0 || len(got.Partitions) != tc.count || len(r.calls) != 1 {
				t.Fatalf("result=%+v calls=%v", got, r.calls)
			}
			p := got.Partitions[0]
			if p.Cluster != "target" || len(p.State) != 1 || p.State[0] != "UP" || !p.DefaultTime.Set {
				t.Fatalf("partition=%+v", p)
			}
			if tc.file == "site-02.json" && !p.MaxTime.Infinite {
				t.Fatal("lost infinite max time")
			}
		})
	}
}
func TestPartitionClusterFailures(t *testing.T) {
	r := newFakeRunner()
	r.responses["scontrol -M a show partition --json"] = []byte(`{"meta":{"slurm":{"version":{"major":"26","minor":"05","micro":"2"}}},"partitions":[]}`)
	r.errors["scontrol -M b show partition --json"] = errors.New("offline")
	result := ListPartitions(context.Background(), r, []model.Association{{Cluster: "b"}, {Cluster: "a"}, {Cluster: "b"}, {}})
	if !reflect.DeepEqual(result.Clusters, []string{"a"}) || len(result.Errors) != 1 || result.Errors[0].Cluster != "b" {
		t.Fatalf("%+v", result)
	}
	for _, data := range []string{"", "{", `{}`, `{"meta":{"slurm":{"version":{"major":"24","minor":"11"}}}}`, `{"meta":{"slurm":{"version":{"major":"26","minor":"05"}}},"errors":[{"error":"denied"}]}`} {
		r.responses["scontrol -M a show partition --json"] = []byte(data)
		got := ListPartitions(context.Background(), r, []model.Association{{Cluster: "a"}})
		if len(got.Errors) != 1 || len(got.Clusters) != 0 {
			t.Fatalf("accepted %q: %+v", data, got)
		}
	}
}
