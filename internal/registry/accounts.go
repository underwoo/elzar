package registry

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/underwoo/elzar/internal/model"
)

type AccountColumn struct {
	ID, Header      string
	Width, MinWidth int
	Value           func(model.AccountRow) string
	Compare         func(model.AccountRow, model.AccountRow) int
	Missing         func(model.AccountRow) bool
}

func AccountColumns() []AccountColumn {
	text := func(id, header string, width int, value func(model.AccountRow) string) AccountColumn {
		return AccountColumn{ID: id, Header: header, Width: width, MinWidth: 4, Value: value, Compare: func(a, b model.AccountRow) int { return strings.Compare(value(a), value(b)) }}
	}
	float := func(id, header string, value func(model.AccountRow) model.OptionalFloat) AccountColumn {
		c := text(id, header, 12, func(r model.AccountRow) string {
			v := value(r)
			if v.Infinite {
				return "∞"
			}
			if !v.Set {
				return "—"
			}
			return fmt.Sprintf("%.4g", v.Number)
		})
		c.Missing = func(r model.AccountRow) bool { v := value(r); return !v.Set && !v.Infinite }
		c.Compare = func(a, b model.AccountRow) int {
			x, y := value(a), value(b)
			if x.Infinite != y.Infinite {
				if x.Infinite {
					return 1
				}
				return -1
			}
			return cmp.Compare(x.Number, y.Number)
		}
		return c
	}
	raw := text("raw_shares", "RawShares", 12, func(r model.AccountRow) string { return r.RawShares.String() })
	raw.Missing = func(r model.AccountRow) bool { return !r.RawShares.Set && !r.RawShares.Infinite }
	raw.Compare = func(a, b model.AccountRow) int {
		if a.RawShares.Infinite != b.RawShares.Infinite {
			if a.RawShares.Infinite {
				return 1
			}
			return -1
		}
		return cmp.Compare(a.RawShares.Number, b.RawShares.Number)
	}
	usage := text("raw_usage", "RawUsage", 14, func(r model.AccountRow) string { return fmt.Sprint(r.RawUsage) })
	usage.Compare = func(a, b model.AccountRow) int { return cmp.Compare(a.RawUsage, b.RawUsage) }
	columns := []AccountColumn{
		text("cluster", "Cluster", 12, func(r model.AccountRow) string { return r.Cluster }),
		text("account", "Account", 18, func(r model.AccountRow) string { return r.Account }),
		text("parent", "Parent", 18, func(r model.AccountRow) string { return r.Parent }),
		text("you", "You", 8, func(r model.AccountRow) string { return r.You }), raw,
		float("norm_shares", "NormShares", func(r model.AccountRow) model.OptionalFloat { return r.NormShares }), usage,
		float("effective_usage", "EffectvUsage", func(r model.AccountRow) model.OptionalFloat { return r.EffectvUsage }),
		float("fairshare", "FairShare", func(r model.AccountRow) model.OptionalFloat { return r.FairShare }),
		float("level", "Level", func(r model.AccountRow) model.OptionalFloat { return r.Level }),
		text("qos", "QOS", 20, func(r model.AccountRow) string { return strings.Join(r.QOS, ",") }),
	}
	for i := range columns {
		if columns[i].ID == "fairshare" {
			value := columns[i].Value
			columns[i].Value = func(r model.AccountRow) string {
				if r.You == "" {
					return ""
				}
				return value(r)
			}
		}
	}
	return columns
}
func AccountColumnsForIDs(ids []string) ([]AccountColumn, []string) {
	all := AccountColumns()
	if ids == nil {
		return all, nil
	}
	byID := map[string]AccountColumn{}
	for _, c := range all {
		byID[c.ID] = c
	}
	var result []AccountColumn
	var unknown []string
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		if c, ok := byID[id]; ok {
			result = append(result, c)
		} else {
			unknown = append(unknown, id)
		}
	}
	return result, unknown
}
