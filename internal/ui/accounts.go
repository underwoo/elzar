package ui

import (
	"context"
	"fmt"
	"os/user"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/underwoo/elzar/internal/config"
	"github.com/underwoo/elzar/internal/model"
	"github.com/underwoo/elzar/internal/registry"
	"github.com/underwoo/elzar/internal/slurm"
)

type accountsState struct {
	rows, visible     []model.AccountRow
	shares            []model.Share
	associations      []model.Association
	qos               []model.QOS
	partitions        []model.Partition
	partitionClusters map[string]bool
	partitionErrors   map[string]error
	cursor, scroll    int
	loading           bool
	err               error
	updated           time.Time
	sort              columnSort
	clusters          map[string]bool
	picker            string
	detailRow         model.AccountRow
}
type accountsTick struct{}
type accountsResult struct {
	shares                     slurm.SharesResult
	associations               slurm.AssociationsResult
	qos                        slurm.QOSResult
	partitions                 slurm.PartitionsResult
	shareErr, assocErr, qosErr error
	at                         time.Time
	username                   string
}

func clusterSelection(value string) map[string]bool {
	selected := map[string]bool{}
	for _, c := range strings.Split(value, ",") {
		c = strings.TrimSpace(c)
		if c == "all" {
			return map[string]bool{}
		}
		if c != "" {
			selected[c] = true
		}
	}
	return selected
}
func (a *App) scheduleAccountsTick() tea.Cmd {
	return tea.Tick(a.options.AccountRefreshInterval, func(time.Time) tea.Msg { return accountsTick{} })
}
func (a *App) startAccountsFetch() tea.Cmd {
	if a.accounts.loading || a.runner == nil {
		return nil
	}
	a.accounts.loading = true
	runner, clock, username := a.runner, a.clock, a.options.Username
	return func() tea.Msg {
		if username == "" {
			u, err := user.Current()
			if err != nil {
				return accountsResult{shareErr: fmt.Errorf("identify current user: %w", err), at: clock()}
			}
			username = u.Username
		}
		shares, e1 := slurm.ListShares(context.Background(), runner)
		associations, e2 := slurm.ListAssociations(context.Background(), runner, username)
		qos, e3 := slurm.ListQOS(context.Background(), runner)
		partitions := slurm.PartitionsResult{}
		if e2 == nil {
			partitions = slurm.ListPartitions(context.Background(), runner, associations.Associations)
		}
		return accountsResult{partitions: partitions, shares: shares, associations: associations, qos: qos, shareErr: e1, assocErr: e2, qosErr: e3, at: clock(), username: username}
	}
}
func buildAccountRows(shares []model.Share, associations []model.Association, username string) []model.AccountRow {
	members := map[string]model.Association{}
	factors := map[string]model.Share{}
	for _, assoc := range associations {
		if assoc.User == username {
			members[assoc.Cluster+"\x00"+assoc.Account] = assoc
		}
	}
	for _, s := range shares {
		if s.Type == "USER" && s.User == username {
			factors[s.Cluster+"\x00"+s.Account] = s
		}
	}
	seen := map[string]bool{}
	rows := []model.AccountRow{}
	for _, s := range shares {
		if s.Type != "ASSOCIATION" {
			continue
		}
		row := model.AccountRow{Share: s}
		k := row.Key()
		if seen[k] {
			continue
		}
		seen[k] = true
		row.FairShare = model.OptionalFloat{}
		if assoc, ok := members[k]; ok {
			row.You = "member"
			if assoc.IsDefault {
				row.You = "default"
			}
			row.QOS = assoc.QOS
			row.Level = model.OptionalFloat{}
			if factor, ok := factors[k]; ok {
				row.FairShare = factor.FairShare
				row.Level = factor.Level
			}
		}
		rows = append(rows, row)
	}
	return rows
}
func (a *App) acceptAccounts(msg accountsResult) {
	a.accounts.loading = false
	a.accounts.err = msg.shareErr
	for _, item := range []struct {
		label string
		err   error
	}{{"shares", msg.shareErr}, {"associations", msg.assocErr}, {"QOS", msg.qosErr}} {
		if item.err != nil {
			a.addMessage("Accounts "+item.label+": "+item.err.Error(), messageError)
			if a.accounts.err == nil {
				a.accounts.err = item.err
			}
		}
	}
	for _, e := range msg.shares.Errors {
		a.addMessage("Accounts: "+e.Error(), messageError)
	}
	for _, w := range append(append(msg.shares.Warnings, msg.associations.Warnings...), msg.qos.Warnings...) {
		a.addMessage("Accounts: "+w, messageError)
	}
	if len(msg.shares.Errors) > 0 && a.accounts.err == nil {
		a.accounts.err = fmt.Errorf("%d share errors; see %s", len(msg.shares.Errors), a.statusMessageHint())
	}
	if msg.assocErr == nil {
		a.accounts.partitionClusters = map[string]bool{}
		a.accounts.partitionErrors = map[string]error{}
		a.accounts.partitions = msg.partitions.Partitions
		for _, cluster := range msg.partitions.Clusters {
			a.accounts.partitionClusters[cluster] = true
		}
		for _, e := range msg.partitions.Errors {
			a.accounts.partitionErrors[e.Cluster] = e.Err
			a.addMessage("Accounts partitions: "+e.Error(), messageError)
			if a.accounts.err == nil {
				a.accounts.err = fmt.Errorf("partition query failed; see %s", a.statusMessageHint())
			}
		}
		for _, w := range msg.partitions.Warnings {
			a.addMessage("Accounts partitions: "+w, messageError)
		}
		a.accounts.associations = msg.associations.Associations
	}
	if msg.qosErr == nil {
		a.accounts.qos = msg.qos.QOS
	}
	if msg.shareErr == nil {
		// Retain old rows only for clusters that failed; successful clusters replace
		// their previous snapshot, including when the new snapshot is empty.
		failed := map[string]bool{}
		unscoped := false
		for _, e := range msg.shares.Errors {
			if e.Cluster == "" {
				unscoped = true
			} else {
				failed[e.Cluster] = true
			}
		}
		shares := append([]model.Share(nil), msg.shares.Shares...)
		succeeded := map[string]bool{}
		for _, s := range shares {
			succeeded[s.Cluster] = true
		}
		for _, old := range a.accounts.shares {
			if failed[old.Cluster] || unscoped && !succeeded[old.Cluster] {
				shares = append(shares, old)
			}
		}
		a.accounts.shares = shares
		a.accounts.updated = msg.at
	}
	if msg.assocErr != nil {
		a.accounts.partitionErrors = nil
		a.accounts.partitionClusters = nil
		a.accounts.partitions = nil
	}
	a.accounts.rows = buildAccountRows(a.accounts.shares, a.accounts.associations, msg.username)
	a.recomputeAccounts()
}
func (a *App) accountColumns() []registry.AccountColumn {
	cols, _ := registry.AccountColumnsForIDs(a.options.AccountColumns)
	return cols
}
func (a *App) recomputeAccounts() {
	selected := ""
	old := a.accounts.visible
	if a.accounts.cursor >= 0 && a.accounts.cursor < len(old) {
		selected = old[a.accounts.cursor].Key()
	}
	cols := a.accountColumns()
	visible := []model.AccountRow{}
	for _, row := range a.accounts.rows {
		if len(a.accounts.clusters) > 0 && !a.accounts.clusters[row.Cluster] {
			continue
		}
		matches := a.filterRegex == nil
		if !matches {
			for _, col := range cols {
				if a.filterRegex.MatchString(col.Value(row)) {
					matches = true
					break
				}
			}
		}
		if matches {
			visible = append(visible, row)
		}
	}
	state := a.accounts.sort
	if state.active {
		for _, c := range cols {
			if c.ID == state.columnID {
				sort.SliceStable(visible, func(i, j int) bool {
					x, y := visible[i], visible[j]
					if c.Missing != nil && c.Missing(x) != c.Missing(y) {
						return !c.Missing(x)
					}
					n := c.Compare(x, y)
					if n == 0 {
						return x.Key() < y.Key()
					}
					if state.descending {
						return n > 0
					}
					return n < 0
				})
				break
			}
		}
	}
	a.accounts.visible = visible
	for i, row := range visible {
		if row.Key() == selected {
			a.accounts.cursor = i
			break
		}
	}
	a.keepAccountsCursorVisible()
}
func (a *App) keepAccountsCursorVisible() {
	n := len(a.accounts.visible)
	if n == 0 {
		a.accounts.cursor = 0
		a.accounts.scroll = 0
		return
	}
	a.accounts.cursor = max(0, min(n-1, a.accounts.cursor))
	a.accounts.scroll = max(0, min(max(0, n-a.tableRowCapacity()), a.accounts.scroll))
	if a.accounts.cursor < a.accounts.scroll {
		a.accounts.scroll = a.accounts.cursor
	}
	if a.accounts.cursor >= a.accounts.scroll+a.tableRowCapacity() {
		a.accounts.scroll = a.accounts.cursor - a.tableRowCapacity() + 1
	}
}
func (a *App) beginAccountsPicker(kind string) {
	a.accounts.picker = kind
	a.pickerColumns = kind == "columns"
	items := []string{}
	checked := []bool{}
	switch kind {
	case "clusters":
		names := map[string]bool{}
		for _, r := range a.accounts.rows {
			names[r.Cluster] = true
		}
		for c := range a.accounts.clusters {
			names[c] = true
		}
		clusters := []string{}
		for c := range names {
			clusters = append(clusters, c)
		}
		sort.Strings(clusters)
		items = append([]string{"All"}, clusters...)
		checked = make([]bool, len(items))
		all := len(a.accounts.clusters) == 0
		checked[0] = all
		for i, c := range clusters {
			checked[i+1] = all || a.accounts.clusters[c]
		}
	case "columns":
		seen := map[string]bool{}
		for _, c := range a.accountColumns() {
			items = append(items, c.ID+"  "+c.Header)
			checked = append(checked, true)
			seen[c.ID] = true
		}
		for _, c := range registry.AccountColumns() {
			if !seen[c.ID] {
				items = append(items, c.ID+"  "+c.Header)
				checked = append(checked, false)
			}
		}
	default:
		for _, c := range a.accountColumns() {
			items = append(items, c.Header)
		}
	}
	title := map[string]string{"clusters": "Choose clusters (empty means All)", "columns": "Choose columns (J/K reorder)", "sort": "Sort by column"}[kind]
	a.picker = newListPicker(title, items, 0)
	if kind != "sort" {
		a.picker.SetChecked(checked)
	}
	a.mode = modePicker
}
func (a *App) handleAccountsPicker(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		a.mode = modeList
	case "up", "k":
		a.picker.Move(-1)
	case "down", "j":
		a.picker.Move(1)
	case "pgup", "ctrl+b":
		a.picker.Move(-a.pageSize())
	case "pgdown", "ctrl+f":
		a.picker.Move(a.pageSize())
	case "J":
		if a.accounts.picker == "columns" {
			a.picker.MoveItem(1)
		}
	case "K":
		if a.accounts.picker == "columns" {
			a.picker.MoveItem(-1)
		}
	case " ":
		if a.accounts.picker == "sort" {
			a.picker.Move(a.pageSize())
			break
		}
		if a.accounts.picker == "clusters" {
			checks := a.picker.Checked()
			index := a.picker.Cursor()
			if index == 0 {
				value := !checks[0]
				for i := range checks {
					checks[i] = value
				}
			} else {
				checks[index] = !checks[index]
				checks[0] = true
				for _, v := range checks[1:] {
					checks[0] = checks[0] && v
				}
			}
			a.picker.SetChecked(checks)
		} else {
			a.picker.Toggle()
		}
	case "enter", "w":
		switch a.accounts.picker {
		case "clusters":
			a.accounts.clusters = map[string]bool{}
			items, checks := a.picker.Items(), a.picker.Checked()
			if !checks[0] {
				for i, c := range items {
					if i > 0 && checks[i] {
						a.accounts.clusters[c] = true
					}
				}
			}
		case "sort":
			cols := a.accountColumns()
			i := a.picker.Cursor()
			if i < len(cols) {
				s := &a.accounts.sort
				if s.active && s.columnID == cols[i].ID {
					s.descending = !s.descending
				} else {
					*s = columnSort{columnID: cols[i].ID, active: true}
				}
			}
		case "columns":
			ids := []string{}
			for i, c := range a.picker.Items() {
				if a.picker.checked[i] {
					id, _, _ := strings.Cut(c, "  ")
					ids = append(ids, id)
				}
			}
			queue, recent := a.options.QueueColumns, a.options.RecentColumns
			if queue == nil {
				queue = columnIDs(registry.QueueTab)
			}
			if recent == nil {
				recent = columnIDs(registry.RecentTab)
			}
			if err := config.SaveColumns(a.options.ConfigPath, queue, recent, ids); err != nil {
				a.setActionStatus("could not save columns: " + err.Error())
			} else {
				a.options.AccountColumns = ids
				a.refreshColumnWarnings()
			}
		}
		a.recomputeAccounts()
		a.mode = modeList
	}
	return nil
}
func (a *App) handleAccountsList(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "q", "ctrl+c":
		return tea.Quit
	case "/":
		return a.beginFilter()
	case "s":
		a.beginAccountsPicker("sort")
	case "c":
		a.beginAccountsPicker("columns")
	case "enter":
		if a.accounts.cursor < len(a.accounts.visible) {
			a.accounts.detailRow = a.accounts.visible[a.accounts.cursor]
			a.detailScroll = 0
			a.mode = modeDetail
		}
	case "M":
		a.beginAccountsPicker("clusters")
	case "r":
		return a.startAccountsFetch()
	case "tab", "shift+tab":
		a.toggleTabKey(msg.String())
	case "esc":
		a.clearFilter()
	default:
		return a.handlePluginKey(msg)
	}
	return nil
}
func (a *App) clusterStatus() string {
	if len(a.accounts.clusters) == 0 {
		return "all"
	}
	names := []string{}
	for c := range a.accounts.clusters {
		names = append(names, c)
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
func (a *App) renderAccounts(width int) string {
	var out strings.Builder
	fmt.Fprintln(&out, titleStyle.Render("elzar")+"  "+mutedStyle.Render("all accounts"))
	fmt.Fprintln(&out, a.tabLine())
	cols := a.accountColumns()
	layout := make([]registry.Column, len(cols))
	for i, c := range cols {
		layout[i] = registry.Column{ID: c.ID, Width: c.Width, MinWidth: c.MinWidth}
		if a.accounts.sort.active && a.accounts.sort.columnID == c.ID {
			if a.accounts.sort.descending {
				cols[i].Header += " ▼"
			} else {
				cols[i].Header += " ▲"
			}
		}
	}
	identifierWidth := 0
	for _, c := range cols {
		if c.ID == "account" {
			identifierWidth = len([]rune(c.Header))
			for _, row := range a.accounts.visible {
				identifierWidth = max(identifierWidth, len([]rune(c.Value(row))))
			}
			break
		}
	}
	indices, widths, hidden := tableLayout(layout, width, "account", identifierWidth)
	visible := make([]registry.AccountColumn, len(indices))
	for i, index := range indices {
		visible[i] = cols[index]
	}
	cols = visible
	cells := []string{}
	rules := []string{}
	for i, c := range cols {
		cells = append(cells, pad(c.Header, widths[i]))
		rules = append(rules, strings.Repeat("─", widths[i]))
	}
	fmt.Fprintln(&out, overflowHeader(strings.Join(cells, " "), hidden, width))
	fmt.Fprintln(&out, strings.Join(rules, " "))
	if len(a.accounts.visible) == 0 {
		fmt.Fprintln(&out, "(no accounts)")
	}
	start := a.accounts.scroll
	end := min(len(a.accounts.visible), start+a.tableRowCapacity())
	for row := start; row < end; row++ {
		cells = nil
		for i, c := range cols {
			cells = append(cells, pad(c.Value(a.accounts.visible[row]), widths[i]))
		}
		line := strings.Join(cells, " ")
		if hidden > 0 {
			line = strings.TrimRight(line, " ")
		}
		if row == a.accounts.cursor {
			line = selectedStyle.Render(line)
		}
		fmt.Fprintln(&out, line)
	}
	status := fmt.Sprintf("Accounts: %d of %d  clusters: %s", len(a.accounts.visible), len(a.accounts.rows), a.clusterStatus())
	if a.mode == modeFilter {
		status += "  " + a.filterInput.View()
	} else if a.filterPattern != "" {
		status += "  /" + a.filterPattern
	}
	if a.accounts.loading {
		status += "  refreshing…"
	}
	if !a.accounts.updated.IsZero() {
		status += "  updated " + a.accounts.updated.Format("15:04:05")
	}
	if a.accounts.err != nil {
		status += "  " + a.accounts.err.Error()
	}
	if a.filterError != nil {
		status += "  " + a.filterError.Error()
	}
	if a.columnWarning != "" {
		status += "  " + a.columnWarning
	}
	if a.actionStatus != "" {
		status += "  " + a.actionStatus
	}
	if nav := a.navigationStatus(); nav != "" {
		status = nav + "  " + status
	}
	fmt.Fprintln(&out, mutedStyle.Render(fitStatusLine(status, width, a.statusMessageHint())))
	fmt.Fprint(&out, mutedStyle.Render(fitFooter(width, []string{"enter details", "tab views", "/ filter", "s sort", "c columns", "M clusters", "r refresh", "q quit", a.statusMessageHint() + " messages", "↑/↓ move", "space page", "G bottom", "1G top"})))
	return fitOutput(out.String(), width)
}
func (a *App) tabLine() string {
	labels := []string{}
	for _, item := range []struct {
		tab   registry.Tab
		label string
	}{{registry.QueueTab, "Queue"}, {registry.RecentTab, "Recent"}, {registry.AccountsTab, "Accounts"}} {
		if a.tab == item.tab {
			labels = append(labels, titleStyle.Render("["+item.label+"]"))
		} else {
			labels = append(labels, mutedStyle.Render(item.label))
		}
	}
	return strings.Join(labels, "  ")
}
func (a *App) toggleTabKey(key string) {
	tabs := []registry.Tab{registry.QueueTab, registry.RecentTab, registry.AccountsTab}
	for i, t := range tabs {
		if a.tab == t {
			delta := 1
			if key == "shift+tab" {
				delta = 2
			}
			a.tab = tabs[(i+delta)%3]
			return
		}
	}
}
