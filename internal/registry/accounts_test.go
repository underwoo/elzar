package registry

import (
	"slurmtui/internal/model"
	"testing"
)

func TestAccountNumericColumns(t *testing.T) {
	columns := AccountColumns()
	a := model.AccountRow{Share: model.Share{RawShares: model.NoValNumber{Set: true, Number: 2}, RawUsage: 9007199254740992, NormShares: model.OptionalFloat{Set: true, Number: 0.2}}}
	b := model.AccountRow{Share: model.Share{RawShares: model.NoValNumber{Set: true, Number: 10}, RawUsage: 9007199254740993, NormShares: model.OptionalFloat{Set: true, Number: 0.8}}}
	for _, c := range columns {
		switch c.ID {
		case "raw_shares", "raw_usage", "norm_shares":
			if c.Compare(a, b) >= 0 || c.Compare(b, a) <= 0 {
				t.Fatalf("%s did not compare numerically", c.ID)
			}
		}
	}
	selected, unknown := AccountColumnsForIDs([]string{"account", "you", "account", "bogus"})
	if len(selected) != 2 || len(unknown) != 1 {
		t.Fatal("column resolution")
	}
	for _, c := range columns {
		if c.ID == "fairshare" && c.Value(a) != "" {
			t.Fatal("non-member fairshare must be blank")
		}
	}
}
