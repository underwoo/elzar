package submit

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/underwoo/elzar/internal/model"
)

// HintData holds the M3 association, partition, and QOS records for a user and
// optional target cluster, pre-organized for form pickers (`ctrl+p`) and
// non-blocking limit checks (`Check`).
type HintData struct {
	User    string
	Cluster string
	// Clusters lists all known cluster names across the user's associations and
	// loaded partitions, sorted alphabetically.
	Clusters       []string
	Accounts       []string
	DefaultAccount string
	// PartitionsByAccount maps an account name to the allowed partition names
	// on the selected cluster(s), sorted alphabetically.
	PartitionsByAccount map[string][]string
	// AllPartitions lists all known partition names on the selected cluster(s).
	AllPartitions []string
	// QOSByAccount maps an account name to the user's allowed QOS names for
	// that account (default QOS first, then alphabetical).
	QOSByAccount map[string][]string
	// DefaultQOSByAccount maps an account name to its default QOS name.
	DefaultQOSByAccount map[string]string
	// AllQOS lists all allowed QOS names across the user's associations.
	AllQOS []string

	associations    []model.Association
	partitions      map[string][]model.Partition
	qosByName       map[string]model.QOS
	allAssociations []model.Association
	allPartitions   []model.Partition
	allQOS          []model.QOS
}

// Warning is a non-blocking advisory on a submit form field (sbatch remains
// the final authority).
type Warning struct {
	Field   FieldID
	Message string
}

// Hints builds picker options and limit lookup tables for user and cluster.
// When cluster is empty, Hints scopes to the local cluster inferred from
// partitions/associations so multi-cluster sites never mix partitions or
// association defaults across clusters.
// The user's default account (`Association.IsDefault`) is placed first in
// Accounts, followed by remaining member accounts in alphabetical order.
func Hints(user, cluster string, associations []model.Association, partitions []model.Partition, qosList []model.QOS) HintData {
	cluster = strings.TrimSpace(cluster)
	clusterSet := make(map[string]bool)
	firstAssocCluster := ""
	for _, a := range associations {
		if user != "" && a.User != "" && a.User != user {
			continue
		}
		if c := strings.TrimSpace(a.Cluster); c != "" {
			clusterSet[c] = true
			if firstAssocCluster == "" {
				firstAssocCluster = c
			}
		}
	}
	partClusterSet := make(map[string]bool)
	firstPartCluster := ""
	for _, p := range partitions {
		if c := strings.TrimSpace(p.Cluster); c != "" {
			clusterSet[c] = true
			partClusterSet[c] = true
			if firstPartCluster == "" {
				firstPartCluster = c
			}
		}
	}
	clusters := make([]string, 0, len(clusterSet))
	for c := range clusterSet {
		clusters = append(clusters, c)
	}
	sort.Strings(clusters)

	if cluster == "" {
		switch {
		case len(partClusterSet) == 1:
			cluster = firstPartCluster
		case firstAssocCluster != "":
			cluster = firstAssocCluster
		case firstPartCluster != "":
			cluster = firstPartCluster
		}
	}

	h := HintData{
		User:                user,
		Cluster:             cluster,
		Clusters:            clusters,
		PartitionsByAccount: make(map[string][]string),
		QOSByAccount:        make(map[string][]string),
		DefaultQOSByAccount: make(map[string]string),
		partitions:          make(map[string][]model.Partition),
		qosByName:           make(map[string]model.QOS, len(qosList)),
		allAssociations:     associations,
		allPartitions:       partitions,
		allQOS:              qosList,
	}

	for _, q := range qosList {
		if q.Name != "" {
			h.qosByName[q.Name] = q
		}
	}

	// Filter associations by user (when specified) and effective cluster.
	var filteredAssocs []model.Association
	for _, a := range associations {
		if user != "" && a.User != "" && a.User != user {
			continue
		}
		if cluster != "" && a.Cluster != "" && !clusterMatches(cluster, a.Cluster) {
			continue
		}
		if a.Account == "" {
			continue
		}
		filteredAssocs = append(filteredAssocs, a)
	}
	h.associations = filteredAssocs

	// Filter partitions by effective cluster.
	allPartSet := make(map[string]bool)
	for _, p := range partitions {
		if cluster != "" && p.Cluster != "" && !clusterMatches(cluster, p.Cluster) {
			continue
		}
		if p.Name == "" {
			continue
		}
		h.partitions[p.Name] = append(h.partitions[p.Name], p)
		allPartSet[p.Name] = true
	}
	for name := range allPartSet {
		h.AllPartitions = append(h.AllPartitions, name)
	}
	sort.Strings(h.AllPartitions)

	// Collect accounts (default account first, then alphabetical) and per-account QOS.
	accountSet := make(map[string]bool)
	qosPerAcct := make(map[string]map[string]bool)
	allQOSSet := make(map[string]bool)

	for _, a := range filteredAssocs {
		accountSet[a.Account] = true
		if a.IsDefault && h.DefaultAccount == "" {
			h.DefaultAccount = a.Account
		}
		if a.DefaultQOS != "" && h.DefaultQOSByAccount[a.Account] == "" {
			h.DefaultQOSByAccount[a.Account] = a.DefaultQOS
		}
		if qosPerAcct[a.Account] == nil {
			qosPerAcct[a.Account] = make(map[string]bool)
		}
		for _, q := range a.QOS {
			if q != "" {
				qosPerAcct[a.Account][q] = true
				allQOSSet[q] = true
			}
		}
	}

	var otherAccounts []string
	for acct := range accountSet {
		if acct == h.DefaultAccount {
			continue
		}
		otherAccounts = append(otherAccounts, acct)
	}
	sort.Strings(otherAccounts)
	if h.DefaultAccount != "" {
		h.Accounts = append([]string{h.DefaultAccount}, otherAccounts...)
	} else {
		h.Accounts = otherAccounts
	}

	for q := range allQOSSet {
		h.AllQOS = append(h.AllQOS, q)
	}
	sort.Strings(h.AllQOS)

	for _, acct := range h.Accounts {
		defQOS := h.DefaultQOSByAccount[acct]
		var restQOS []string
		for q := range qosPerAcct[acct] {
			if q == defQOS {
				continue
			}
			restQOS = append(restQOS, q)
		}
		sort.Strings(restQOS)
		if defQOS != "" && qosPerAcct[acct][defQOS] {
			h.QOSByAccount[acct] = append([]string{defQOS}, restQOS...)
		} else {
			h.QOSByAccount[acct] = restQOS
		}

		// Allowed partitions for this account across matching associations.
		partSet := make(map[string]bool)
		for _, pList := range h.partitions {
			for _, p := range pList {
				for _, a := range filteredAssocs {
					if a.Account == acct && pAllowsAssociation(p, a) {
						partSet[p.Name] = true
						break
					}
				}
			}
		}
		var allowedParts []string
		for name := range partSet {
			allowedParts = append(allowedParts, name)
		}
		sort.Strings(allowedParts)
		h.PartitionsByAccount[acct] = allowedParts
	}

	return h
}

// PartitionsForAccount returns the allowed partition names for account (or
// for the user's default/all accounts when account is empty).
func (h HintData) PartitionsForAccount(account string) []string {
	account = strings.TrimSpace(account)
	if account == "" {
		account = h.DefaultAccount
	}
	if account != "" {
		if parts, ok := h.PartitionsByAccount[account]; ok {
			return slices.Clone(parts)
		}
	}
	// Union of allowed partitions across all member accounts, falling back to AllPartitions.
	seen := make(map[string]bool)
	for _, parts := range h.PartitionsByAccount {
		for _, p := range parts {
			seen[p] = true
		}
	}
	if len(seen) == 0 {
		return slices.Clone(h.AllPartitions)
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// QOSFor returns the allowed QOS names for the given account and partition.
// When partition has an explicit AllowedQOS or DeniedQOS list, the account's
// allowed QOS list is intersected with the partition's QOS rules.
func (h HintData) QOSFor(account, partition string) []string {
	account = strings.TrimSpace(account)
	partition = strings.TrimSpace(partition)
	if account == "" {
		account = h.DefaultAccount
	}
	var base []string
	if account != "" {
		base = slices.Clone(h.QOSByAccount[account])
	} else {
		base = slices.Clone(h.AllQOS)
	}
	if partition == "" || len(base) == 0 {
		return base
	}
	pList, ok := h.partitions[partition]
	if !ok || len(pList) == 0 {
		return base
	}
	var filtered []string
	for _, q := range base {
		for _, p := range pList {
			if partitionAllowsQOS(p, q) {
				filtered = append(filtered, q)
				break
			}
		}
	}
	return filtered
}

func clusterMatches(target, item string) bool {
	if target == "" || strings.EqualFold(target, "all") {
		return true
	}
	for _, c := range strings.Split(target, ",") {
		if strings.TrimSpace(c) == item {
			return true
		}
	}
	return false
}

// Check evaluates effective field values against hints (scoped to the effective
// cluster) and returns non-blocking warnings (never errors — sbatch is the
// final authority):
//   - cluster not in user's known association/partition clusters
//   - account not in user's associations on the effective cluster
//   - partition not in the effective cluster's partitions or not allowed for the effective account
//   - QOS not allowed for the effective account or partition
//   - time limit exceeds QOS MaxWall or partition MaxTime
//   - node count exceeds partition MaxNodes or QOS MaxTRESPerJob node limit
func Check(values map[FieldID]string, hints HintData) []Warning {
	var warnings []Warning

	reqCluster := strings.TrimSpace(values[FieldClusters])
	if reqCluster != "" {
		if reqCluster != hints.Cluster && (len(hints.allAssociations) > 0 || len(hints.allPartitions) > 0) {
			hints = Hints(hints.User, reqCluster, hints.allAssociations, hints.allPartitions, hints.allQOS)
		}
		if len(hints.Clusters) > 0 && !strings.EqualFold(reqCluster, "all") {
			for _, c := range strings.Split(reqCluster, ",") {
				c = strings.TrimSpace(c)
				if c != "" && !slices.Contains(hints.Clusters, c) {
					warnings = append(warnings, Warning{
						Field:   FieldClusters,
						Message: fmt.Sprintf("cluster %q is not in your associations", c),
					})
				}
			}
		}
	}

	account := strings.TrimSpace(values[FieldAccount])
	partition := strings.TrimSpace(values[FieldPartition])
	qosName := strings.TrimSpace(values[FieldQOS])
	timeStr := strings.TrimSpace(values[FieldTime])
	nodesStr := strings.TrimSpace(values[FieldNodes])

	effectiveAccount := account
	if effectiveAccount == "" {
		effectiveAccount = hints.DefaultAccount
	}

	accountValid := true
	if len(hints.associations) > 0 && account != "" {
		if !slices.Contains(hints.Accounts, account) {
			accountValid = false
			warnings = append(warnings, Warning{
				Field:   FieldAccount,
				Message: fmt.Sprintf("account %q is not in your associations", account),
			})
		}
	}

	// Partition access check.
	if partition != "" && (len(hints.partitions) > 0 || len(hints.allPartitions) > 0) {
		pList, exists := hints.partitions[partition]
		if !exists {
			warnings = append(warnings, Warning{
				Field:   FieldPartition,
				Message: fmt.Sprintf("partition %q is not in cluster partitions", partition),
			})
		} else if accountValid && effectiveAccount != "" && len(hints.associations) > 0 {
			allowedParts := hints.PartitionsByAccount[effectiveAccount]
			if !slices.Contains(allowedParts, partition) {
				warnings = append(warnings, Warning{
					Field:   FieldPartition,
					Message: fmt.Sprintf("partition %q is not allowed for account %q", partition, effectiveAccount),
				})
			} else if len(pList) > 0 && isPartitionDown(pList) {
				warnings = append(warnings, Warning{
					Field:   FieldPartition,
					Message: fmt.Sprintf("partition %q state is %s", partition, strings.Join(pList[0].State, ",")),
				})
			}
		}
	}

	// QOS access check.
	effectiveQOS := qosName
	if effectiveQOS == "" && effectiveAccount != "" {
		effectiveQOS = hints.DefaultQOSByAccount[effectiveAccount]
	}
	if qosName != "" {
		qosAllowedByAccount := true
		if accountValid && effectiveAccount != "" && len(hints.associations) > 0 {
			allowedQOS := hints.QOSByAccount[effectiveAccount]
			if !slices.Contains(allowedQOS, qosName) {
				qosAllowedByAccount = false
				warnings = append(warnings, Warning{
					Field:   FieldQOS,
					Message: fmt.Sprintf("QOS %q is not allowed for account %q", qosName, effectiveAccount),
				})
			}
		}
		if qosAllowedByAccount && partition != "" {
			if pList, ok := hints.partitions[partition]; ok && len(pList) > 0 {
				allowedOnPart := false
				for _, p := range pList {
					if partitionAllowsQOS(p, qosName) {
						allowedOnPart = true
						break
					}
				}
				if !allowedOnPart {
					warnings = append(warnings, Warning{
						Field:   FieldQOS,
						Message: fmt.Sprintf("QOS %q is not allowed on partition %q", qosName, partition),
					})
				}
			}
		}
	}

	// Time limit vs. QOS MaxWall and Partition MaxTime.
	if timeStr != "" {
		if mins, infinite, ok := ParseTimeMinutes(timeStr); ok {
			if effectiveQOS != "" {
				if q, exists := hints.qosByName[effectiveQOS]; exists {
					if limit, hasLimit := q.MaxWall.Value(); hasLimit && (infinite || mins > limit) {
						warnings = append(warnings, Warning{
							Field:   FieldTime,
							Message: fmt.Sprintf("time %s exceeds QOS %q MaxWall (%d min)", formatReqMinutes(timeStr, mins, infinite), effectiveQOS, limit),
						})
					}
				}
			}
			if partition != "" {
				if pList, exists := hints.partitions[partition]; exists {
					if limit, hasLimit := minFiniteLimit(pList, func(p model.Partition) model.NoValNumber { return p.MaxTime }); hasLimit && (infinite || mins > limit) {
						warnings = append(warnings, Warning{
							Field:   FieldTime,
							Message: fmt.Sprintf("time %s exceeds partition %q MaxTime (%d min)", formatReqMinutes(timeStr, mins, infinite), partition, limit),
						})
					}
				}
			}
		}
	}

	// Nodes vs. Partition MaxNodes and QOS MaxTRESPerJob node limit.
	if nodesStr != "" {
		if minNodes, ok := parseMinNodes(nodesStr); ok {
			if partition != "" {
				if pList, exists := hints.partitions[partition]; exists {
					if limit, hasLimit := minFiniteLimit(pList, func(p model.Partition) model.NoValNumber { return p.MaxNodes }); hasLimit && minNodes > limit {
						warnings = append(warnings, Warning{
							Field:   FieldNodes,
							Message: fmt.Sprintf("nodes %s exceeds partition %q MaxNodes (%d)", nodesStr, partition, limit),
						})
					}
				}
			}
			if effectiveQOS != "" {
				if q, exists := hints.qosByName[effectiveQOS]; exists {
					if limit, hasLimit := tresLimit(q.MaxTRESPerJob, "node", ""); hasLimit && minNodes > limit {
						warnings = append(warnings, Warning{
							Field:   FieldNodes,
							Message: fmt.Sprintf("nodes %s exceeds QOS %q MaxTRESPerJob node limit (%d)", nodesStr, effectiveQOS, limit),
						})
					}
				}
			}
		}
	}

	return warnings
}

// CheckPlan is a convenience wrapper around Check that extracts effective
// values from a resolved Plan.
func CheckPlan(plan Plan, hints HintData) []Warning {
	values := make(map[FieldID]string, len(plan.Fields))
	for _, f := range plan.Fields {
		if f.Value != "" {
			values[f.Spec.ID] = f.Value
		}
	}
	return Check(values, hints)
}

// ParseTimeMinutes parses a Slurm time limit string into rounded-up minutes,
// matching Slurm 25.11 `src/common/parse_time.c:744-792` (`time_str2mins` /
// `time_str2secs`) and `src/common/slurm_opt.c:3741-3747` (`0` means INFINITE):
//   - "INFINITE", "UNLIMITED", "-1", or "0" -> infinite=true, ok=true
//   - "M" -> M minutes
//   - "M:S" -> ceil((M*60 + S) / 60) minutes
//   - "H:M:S" -> ceil((H*3600 + M*60 + S) / 60) minutes
//   - "D-H", "D-H:M", "D-H:M:S" -> ceil((D*86400 + H*3600 + M*60 + S) / 60) minutes
func ParseTimeMinutes(value string) (minutes int64, infinite bool, ok bool) {
	s := strings.TrimSpace(value)
	if s == "" {
		return 0, false, false
	}
	if s == "-1" || strings.EqualFold(s, "INFINITE") || strings.EqualFold(s, "UNLIMITED") {
		return 0, true, true
	}

	var days, hours, mins, secs int64
	if dayPart, rest, hasDash := strings.Cut(s, "-"); hasDash {
		if strings.Contains(dayPart, ":") || strings.Contains(rest, "-") {
			return 0, false, false
		}
		d, err := parseNonNegInt(dayPart)
		if err != nil {
			return 0, false, false
		}
		days = d
		parts := strings.Split(rest, ":")
		if len(parts) < 1 || len(parts) > 3 {
			return 0, false, false
		}
		vals := make([]int64, len(parts))
		for i, p := range parts {
			v, err := parseNonNegInt(p)
			if err != nil {
				return 0, false, false
			}
			vals[i] = v
		}
		hours = vals[0]
		if len(vals) >= 2 {
			mins = vals[1]
		}
		if len(vals) == 3 {
			secs = vals[2]
		}
	} else {
		parts := strings.Split(s, ":")
		if len(parts) < 1 || len(parts) > 3 {
			return 0, false, false
		}
		vals := make([]int64, len(parts))
		for i, p := range parts {
			v, err := parseNonNegInt(p)
			if err != nil {
				return 0, false, false
			}
			vals[i] = v
		}
		switch len(vals) {
		case 1:
			mins = vals[0]
		case 2:
			mins = vals[0]
			secs = vals[1]
		case 3:
			hours = vals[0]
			mins = vals[1]
			secs = vals[2]
		}
	}

	totalSecs := days*86400 + hours*3600 + mins*60 + secs
	if totalSecs == 0 {
		// Slurm 25.11 src/common/slurm_opt.c:3745-3747: time_limit == 0 -> INFINITE.
		return 0, true, true
	}
	return (totalSecs + 59) / 60, false, true
}

func parseNonNegInt(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, fmt.Errorf("non-digit")
		}
	}
	return strconv.ParseInt(s, 10, 64)
}

func parseMinNodes(value string) (int64, bool) {
	s := strings.TrimSpace(value)
	if s == "" {
		return 0, false
	}
	minPart, _, _ := strings.Cut(s, "-")
	n, err := parseNonNegInt(minPart)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func formatReqMinutes(raw string, mins int64, infinite bool) string {
	if infinite {
		return fmt.Sprintf("%s (UNLIMITED)", raw)
	}
	return fmt.Sprintf("%s (%d min)", raw, mins)
}

func pAllowsAssociation(p model.Partition, a model.Association) bool {
	if p.Cluster == "" && a.Cluster != "" {
		p.Cluster = a.Cluster
	}
	return p.Allows(a)
}

func partitionAllowsQOS(p model.Partition, qos string) bool {
	if len(p.AllowedQOS) == 0 && len(p.DeniedQOS) == 0 {
		return true
	}
	if len(p.AllowedQOS) > 0 {
		return slices.Contains(p.AllowedQOS, "ALL") || slices.Contains(p.AllowedQOS, qos)
	}
	return !slices.Contains(p.DeniedQOS, "ALL") && !slices.Contains(p.DeniedQOS, qos)
}

func minFiniteLimit(partitions []model.Partition, get func(model.Partition) model.NoValNumber) (int64, bool) {
	var minVal int64
	found := false
	for _, p := range partitions {
		if v, ok := get(p).Value(); ok {
			if !found || v < minVal {
				minVal = v
				found = true
			}
		}
	}
	return minVal, found
}

func isPartitionDown(partitions []model.Partition) bool {
	for _, p := range partitions {
		if len(p.State) == 0 || slices.Contains(p.State, "UP") {
			return false
		}
	}
	return len(partitions) > 0 && len(partitions[0].State) > 0
}

func tresLimit(items []model.TRES, typ, name string) (int64, bool) {
	for _, item := range items {
		if strings.EqualFold(item.Type, typ) && (name == "" || strings.EqualFold(item.Name, name)) && item.Count > 0 {
			return item.Count, true
		}
	}
	return 0, false
}
