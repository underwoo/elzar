package model

import "strings"

// Partition describes account/QOS access and configured limits, not scheduling availability.
type Partition struct {
	Cluster, Name                                          string
	AllowedAccounts, DeniedAccounts, AllowedQOS, DeniedQOS []string
	MaxTime, DefaultTime, MaxNodes                         NoValNumber
	State                                                  []string
}

// Allows follows Slurm's hierarchical account lists and allow-list precedence.
// See https://slurm.schedmd.com/slurm.conf.html (AllowAccounts and AllowQos).
func (p Partition) Allows(a Association) bool {
	if p.Cluster != a.Cluster {
		return false
	}
	accounts := append([]string{a.Account}, strings.FieldsFunc(a.Lineage, func(r rune) bool { return r == '/' })...)
	if !accessList(p.AllowedAccounts, p.DeniedAccounts, accounts) {
		return false
	}
	if len(p.AllowedQOS) == 0 && len(p.DeniedQOS) == 0 || len(p.AllowedQOS) == 1 && p.AllowedQOS[0] == "ALL" {
		return true
	}
	for _, q := range a.QOS {
		if accessList(p.AllowedQOS, p.DeniedQOS, []string{q}) {
			return true
		}
	}
	return false
}
func accessList(allowed, denied, names []string) bool {
	contains := func(list []string) bool {
		for _, entry := range list {
			if entry == "ALL" {
				return true
			}
			for _, name := range names {
				if entry == name {
					return true
				}
			}
		}
		return false
	}
	if len(allowed) > 0 {
		return contains(allowed)
	}
	return !contains(denied)
}
