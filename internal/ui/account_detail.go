package ui

import (
	"fmt"
	"strings"

	"github.com/underwoo/elzar/internal/model"

	tea "github.com/charmbracelet/bubbletea"
)

func (a *App) handleAccountDetailKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.mode = modeList
		a.detailScroll = 0
	case "tab", "shift+tab":
		a.toggleTabKey(msg.String())
		a.leaveDetailForTabSwitch()
	case "q", "ctrl+c":
		return tea.Quit
	default:
		return a.handlePluginKey(msg)
	}
	return nil
}

func accountFloat(v model.OptionalFloat) string {
	if v.Infinite {
		return "∞"
	}
	if !v.Set {
		return "—"
	}
	return fmt.Sprintf("%.4g", v.Number)
}

func accountTRES(items []model.TRES) string {
	if len(items) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		name := item.Type
		if item.Name != "" {
			if name != "" {
				name += "/"
			}
			name += item.Name
		}
		if name == "" {
			name = fmt.Sprintf("TRES %d", item.ID)
		}
		parts = append(parts, fmt.Sprintf("%s=%d", name, item.Count))
	}
	return strings.Join(parts, ", ")
}

func (a *App) accountDetailLines() []string {
	return append(a.accountSummaryLines(), a.accountPartitionLines()...)
}

func (a *App) accountSummaryLines() []string {
	row := a.accounts.detailRow
	shares := map[string]model.Share{}
	for _, share := range a.accounts.shares {
		if share.Cluster == row.Cluster && share.Type == "ASSOCIATION" {
			shares[share.Account] = share
		}
	}
	chain := []model.Share{row.Share}
	seen := map[string]bool{row.Account: true}
	parent := row.Parent
	warning := ""
	for parent != "" {
		if seen[parent] {
			warning = "Lineage contains a cycle at " + parent
			break
		}
		seen[parent] = true
		share, ok := shares[parent]
		if !ok {
			warning = "Ancestor data unavailable: " + parent
			break
		}
		chain = append(chain, share)
		parent = share.Parent
	}
	lines := []string{"", titleStyle.Render("Lineage (root → account)")}
	if warning != "" {
		lines = append(lines, warning)
	}
	for i := len(chain) - 1; i >= 0; i-- {
		s := chain[i]
		lines = append(lines, s.Account,
			"  RawShares: "+s.RawShares.String()+"  NormShares: "+accountFloat(s.NormShares),
			fmt.Sprintf("  RawUsage: %d  EffectvUsage: %s", s.RawUsage, accountFloat(s.EffectvUsage)))
	}
	lines = append(lines, "", titleStyle.Render("Your association"))
	if row.You == "" {
		return append(lines, "Not a member of this account.", "", titleStyle.Render("Allowed QOS"), "No user association; allowed QOS unavailable.")
	}
	lines = append(lines, "Membership: "+row.You, "FairShare: "+accountFloat(row.FairShare)+"  Level: "+accountFloat(row.Level), "", titleStyle.Render("Allowed QOS"))
	if len(row.QOS) == 0 {
		return append(lines, "No allowed QOS listed.")
	}
	qosByName := map[string]model.QOS{}
	for _, qos := range a.accounts.qos {
		qosByName[qos.Name] = qos
	}
	for _, name := range row.QOS {
		q, ok := qosByName[name]
		lines = append(lines, name)
		if !ok {
			lines = append(lines, "  QOS limits unavailable.")
			continue
		}
		lines = append(lines,
			"  MaxWall (minutes): "+q.MaxWall.String(),
			"  MaxJobs: "+q.MaxJobs.String(),
			"  MaxJobs per user: "+q.MaxJobsPerUser.String(),
			"  MaxJobs per account: "+q.MaxJobsPerAccount.String(),
			"  MaxSubmit per user: "+q.MaxSubmitPerUser.String(),
			"  MaxSubmit per account: "+q.MaxSubmitPerAccount.String(),
			"  MaxTRES per job: "+accountTRES(q.MaxTRESPerJob),
			"  MaxTRES per user: "+accountTRES(q.MaxTRESPerUser),
			"  Priority: "+q.Priority.String()+"  UsageFactor: "+accountFloat(q.UsageFactor))
	}
	return lines
}

func (a *App) renderAccountDetail(out *strings.Builder, width int) {
	row := a.accounts.detailRow
	fmt.Fprintln(out, titleStyle.Render("Account detail: "+row.Cluster+"/"+row.Account))
	lines := a.accountDetailLines()
	available := a.detailPageSize()
	a.detailScroll = min(a.detailScroll, max(0, len(lines)-available))
	for _, line := range lines[a.detailScroll:min(len(lines), a.detailScroll+available)] {
		fmt.Fprintln(out, truncateLine(line, width))
	}
	fmt.Fprint(out, mutedStyle.Render(fitFooter(width, []string{"esc back", "↑/↓ scroll", "space page", "G bottom", "1G top", "tab views", "q quit"})))
}

func (a *App) accountPartitionLines() []string {
	row := a.accounts.detailRow
	lines := []string{"", titleStyle.Render("Partition access (account/QOS)")}
	if row.You == "" {
		return append(lines, "No user association; partition access unavailable.")
	}
	if a.accounts.partitionErrors[row.Cluster] != nil {
		return append(lines, "Partition query failed; see "+a.statusMessageHint()+".")
	}
	if !a.accounts.partitionClusters[row.Cluster] {
		return append(lines, "Partition data unavailable.")
	}
	found := false
	for _, p := range a.accounts.partitions {
		allowed := false
		for _, assoc := range a.accounts.associations {
			if assoc.Cluster == row.Cluster && assoc.Account == row.Account && p.Allows(assoc) {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}
		found = true
		state := strings.Join(p.State, ",")
		if state == "" {
			state = "—"
		}
		lines = append(lines, p.Name+"  State: "+state,
			"  MaxTime (minutes): "+p.MaxTime.String()+"  DefaultTime (minutes): "+p.DefaultTime.String(),
			"  MaxNodes: "+p.MaxNodes.String())
	}
	if !found {
		lines = append(lines, "No partitions match this association's account/QOS.")
	}
	return lines
}
