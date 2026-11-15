package model

// OptionalFloat preserves Slurm's unset/infinite markers for share factors.
type OptionalFloat struct {
	Set      bool    `json:"set"`
	Infinite bool    `json:"infinite"`
	Number   float64 `json:"number"`
}

// Share is an account association or a user's share within its parent account.
// RawShares on USER rows can contain Slurm's inherit-from-parent sentinel.
type Share struct {
	Cluster      string
	Account      string
	User         string
	Parent       string
	Type         string
	RawShares    NoValNumber
	NormShares   OptionalFloat
	RawUsage     uint64
	EffectvUsage OptionalFloat
	FairShare    OptionalFloat
	Level        OptionalFloat
}

type Association struct {
	Cluster    string   `json:"cluster"`
	Account    string   `json:"account"`
	User       string   `json:"user"`
	Parent     string   `json:"parent_account"`
	Lineage    string   `json:"lineage"`
	QOS        []string `json:"qos"`
	DefaultQOS string
	IsDefault  bool `json:"is_default"`
}

// QOS keeps the limits needed for account detail. Wall-clock limits are minutes.
type QOS struct {
	Name                string
	Priority            NoValNumber
	UsageFactor         OptionalFloat
	MaxWall             NoValNumber
	MaxJobs             NoValNumber
	MaxJobsPerUser      NoValNumber
	MaxJobsPerAccount   NoValNumber
	MaxSubmitPerUser    NoValNumber
	MaxSubmitPerAccount NoValNumber
	MaxTRESPerJob       []TRES
	MaxTRESPerUser      []TRES
}
