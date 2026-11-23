package slurm

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/underwoo/elzar/internal/model"
)

func TestBatchIndicatorsFromFixtures(t *testing.T) {
	data, err := os.ReadFile("../../testdata/squeue-02.json")
	if err != nil {
		t.Fatal(err)
	}
	var queue struct {
		Jobs []model.Job `json:"jobs"`
	}
	if err := json.Unmarshal(data, &queue); err != nil {
		t.Fatal(err)
	}
	if len(queue.Jobs) == 0 || queue.Jobs[0].Batch == nil || !*queue.Jobs[0].Batch {
		t.Fatal("missing squeue batch flag")
	}
	data, err = os.ReadFile("../../testdata/sacct_recent.json")
	if err != nil {
		t.Fatal(err)
	}
	var recent sacctResponse
	if err := json.Unmarshal(data, &recent); err != nil {
		t.Fatal(err)
	}
	if len(recent.Jobs) == 0 || recent.Jobs[0].job().Batch == nil || !*recent.Jobs[0].job().Batch {
		t.Fatal("missing accounting batch step")
	}
	if (sacctJob{}).job().Batch != nil {
		t.Fatal("absent batch metadata became false")
	}
	var job model.Job
	if err := json.Unmarshal([]byte(`{"batch_flag":false}`), &job); err != nil {
		t.Fatal(err)
	}
	if job.Batch == nil || *job.Batch {
		t.Fatal("explicit non-batch flag lost")
	}
}
