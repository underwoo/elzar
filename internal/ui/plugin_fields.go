package ui

import (
	"strings"

	"github.com/underwoo/elzar/internal/model"
	"github.com/underwoo/elzar/internal/plugin"
)

// Empty optional fields are unavailable: in particular an empty path must
// never accidentally invoke a pager with stdin or the working directory.
func jobPluginFields(job model.Job) plugin.Fields {
	fields := plugin.Fields{"JobID": job.DisplayID()}
	values := map[string]string{
		"Name": job.Name, "User": job.UserName, "Account": job.Account,
		"Partition": job.Partition, "QOS": job.QOS, "State": strings.Join(stateStrings(job.States), ","),
		"Cluster": job.Cluster, "NodeList": job.NodeList, "FirstNode": firstNode(job.NodeList),
		"WorkDir": job.WorkingDirectory, "StdOut": job.Stdout, "StdErr": job.Stderr,
	}
	for name, value := range values {
		if value != "" {
			fields[name] = value
		}
	}
	if job.Comment != nil {
		fields["Comment"] = *job.Comment
	}
	return fields
}

func accountPluginFields(row model.AccountRow) plugin.Fields {
	return plugin.Fields{"Cluster": row.Cluster, "Account": row.Account, "Parent": row.Parent}
}

// firstNode selects the first entry in each bracket range without expanding
// the hostlist (which can represent thousands of nodes).
func firstNode(nodes string) string {
	var out strings.Builder
	for len(nodes) > 0 {
		i := strings.IndexAny(nodes, "[,")
		if i < 0 {
			out.WriteString(nodes)
			break
		}
		out.WriteString(nodes[:i])
		if nodes[i] == ',' {
			break
		}
		nodes = nodes[i+1:]
		end := strings.IndexByte(nodes, ']')
		if end < 0 {
			return ""
		}
		first := strings.SplitN(strings.SplitN(nodes[:end], ",", 2)[0], "-", 2)[0]
		if first == "" {
			return ""
		}
		out.WriteString(first)
		nodes = nodes[end+1:]
	}
	return out.String()
}
