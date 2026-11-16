package model

// AccountRow is keyed by cluster and account, independent of the job views.
type AccountRow struct {
	Share
	You string
	QOS []string
}

func (r AccountRow) Key() string { return r.Cluster + "\x00" + r.Account }
