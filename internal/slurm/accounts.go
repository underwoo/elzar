package slurm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/underwoo/elzar/internal/model"
)

type ClusterError struct {
	Cluster string
	Err     error
}

func (e ClusterError) Error() string {
	if e.Cluster == "" {
		return e.Err.Error()
	}
	return "cluster " + e.Cluster + ": " + e.Err.Error()
}

type SharesResult struct {
	Shares   []model.Share
	Warnings []string
	Errors   []ClusterError
}

type AssociationsResult struct {
	Associations []model.Association
	Version      Version
	Warnings     []string
}

type QOSResult struct {
	QOS      []model.QOS
	Version  Version
	Warnings []string
}

type accountEnvelope struct {
	Meta struct {
		Slurm struct {
			Version Version `json:"version"`
			Cluster string  `json:"cluster"`
		} `json:"slurm"`
	} `json:"meta"`
	Errors   []slurmMessage `json:"errors"`
	Warnings []slurmMessage `json:"warnings"`
}

func (e accountEnvelope) validate() error {
	v := e.Meta.Slurm.Version
	if v.Major == 0 {
		return fmt.Errorf("missing meta.slurm.version")
	}
	if !v.Supported() {
		return fmt.Errorf("slurm %s is unsupported; elzar requires Slurm 25.11 or newer", v)
	}
	if len(e.Errors) != 0 {
		return fmt.Errorf("slurm reported errors: %s", joinMessages(e.Errors))
	}
	return nil
}
func (e accountEnvelope) warnings() []string {
	var warnings []string
	for _, w := range e.Warnings {
		if s := w.String(); s != "" {
			warnings = append(warnings, s)
		}
	}
	return warnings
}

type shareDocument struct {
	cluster string
	data    []byte
}

// splitShareDocuments recognizes headers only at line boundaries, never inside
// JSON strings. No scanner token limit is imposed on large compact responses.
func splitShareDocuments(data []byte) []shareDocument {
	var docs []shareDocument
	var block bytes.Buffer
	cluster := ""
	header := false
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if bytes.HasPrefix(trimmed, []byte("CLUSTER:")) {
			if header || len(bytes.TrimSpace(block.Bytes())) > 0 {
				docs = append(docs, shareDocument{cluster, bytes.Clone(block.Bytes())})
			}
			cluster = strings.TrimSpace(string(trimmed[len("CLUSTER:"):]))
			header = true
			block.Reset()
		} else {
			block.Write(line)
			block.WriteByte('\n')
		}
	}
	if header || len(bytes.TrimSpace(block.Bytes())) > 0 {
		docs = append(docs, shareDocument{cluster, bytes.Clone(block.Bytes())})
	}
	return docs
}

// ListShares retains successful clusters even when another cluster or the
// command itself fails. Errors are returned as diagnostics for the message log.
// Failure before any stdout (missing binary, timeout, etc.) is a command error.
func ListShares(ctx context.Context, runner Runner) (SharesResult, error) {
	data, runErr := runner.Run(ctx, "sshare", "--json", "-l", "-U", "-M", "all")
	result := SharesResult{Shares: []model.Share{}}
	if len(bytes.TrimSpace(data)) == 0 {
		if runErr != nil {
			return result, fmt.Errorf("run sshare: %w", runErr)
		}
		return result, fmt.Errorf("sshare returned empty output")
	}
	for _, doc := range splitShareDocuments(data) {
		shares, warnings, err := decodeShares(doc.data, doc.cluster)
		if err != nil {
			result.Errors = append(result.Errors, ClusterError{doc.cluster, err})
			continue
		}
		result.Shares = append(result.Shares, shares...)
		for _, w := range warnings {
			if doc.cluster != "" {
				w = "cluster " + doc.cluster + ": " + w
			}
			result.Warnings = append(result.Warnings, w)
		}
	}
	if runErr != nil {
		result.Errors = append(result.Errors, ClusterError{Err: fmt.Errorf("run sshare: %w", runErr)})
	}
	return result, nil
}

func decodeShares(data []byte, cluster string) ([]model.Share, []string, error) {
	var response struct {
		accountEnvelope
		Shares struct {
			Shares []struct {
				Cluster    string              `json:"cluster"`
				Name       string              `json:"name"`
				Parent     string              `json:"parent"`
				Type       []string            `json:"type"`
				Shares     model.NoValNumber   `json:"shares"`
				Normalized model.OptionalFloat `json:"shares_normalized"`
				Usage      uint64              `json:"usage"`
				Effective  model.OptionalFloat `json:"effective_usage"`
				Fairshare  struct {
					Factor model.OptionalFloat `json:"factor"`
					Level  model.OptionalFloat `json:"level"`
				} `json:"fairshare"`
			} `json:"shares"`
		} `json:"shares"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, nil, fmt.Errorf("decode sshare JSON: %w", err)
	}
	if err := response.validate(); err != nil {
		return nil, nil, err
	}
	var shares []model.Share
	for _, row := range response.Shares.Shares {
		rowCluster := row.Cluster
		if rowCluster == "" {
			rowCluster = response.Meta.Slurm.Cluster
		}
		if cluster != "" {
			rowCluster = cluster
		}
		share := model.Share{Cluster: rowCluster, Account: row.Name, Parent: row.Parent,
			RawShares: row.Shares, NormShares: row.Normalized, RawUsage: row.Usage,
			EffectvUsage: row.Effective, FairShare: row.Fairshare.Factor, Level: row.Fairshare.Level}
		for _, kind := range row.Type {
			if kind == "ASSOCIATION" || kind == "USER" {
				share.Type = kind
				break
			}
		}
		if share.Type == "USER" {
			share.User = row.Name
			share.Account = row.Parent
		}
		if share.Type == "" {
			continue
		}
		shares = append(shares, share)
	}
	return shares, response.warnings(), nil
}

func ListAssociations(ctx context.Context, runner Runner, username string) (AssociationsResult, error) {
	if strings.TrimSpace(username) == "" {
		return AssociationsResult{}, fmt.Errorf("association query requires a username")
	}
	data, err := runner.Run(ctx, "sacctmgr", "--json", "show", "assoc", "user="+username)
	if err != nil {
		return AssociationsResult{}, fmt.Errorf("run sacctmgr assoc: %w", err)
	}
	var response struct {
		accountEnvelope
		Associations []struct {
			model.Association
			Default struct {
				QOS string `json:"qos"`
			} `json:"default"`
		} `json:"associations"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return AssociationsResult{}, fmt.Errorf("decode associations JSON: %w", err)
	}
	if err := response.validate(); err != nil {
		return AssociationsResult{}, err
	}
	result := AssociationsResult{Associations: []model.Association{}, Version: response.Meta.Slurm.Version, Warnings: response.warnings()}
	for _, row := range response.Associations {
		row.DefaultQOS = row.Default.QOS
		result.Associations = append(result.Associations, row.Association)
	}
	return result, nil
}

func ListQOS(ctx context.Context, runner Runner) (QOSResult, error) {
	data, err := runner.Run(ctx, "sacctmgr", "--json", "show", "qos")
	if err != nil {
		return QOSResult{}, fmt.Errorf("run sacctmgr qos: %w", err)
	}
	type perIdentity struct {
		Account model.NoValNumber `json:"account"`
		User    model.NoValNumber `json:"user"`
	}
	var response struct {
		accountEnvelope
		QOS []struct {
			Name        string              `json:"name"`
			Priority    model.NoValNumber   `json:"priority"`
			UsageFactor model.OptionalFloat `json:"usage_factor"`
			Limits      struct {
				Max struct {
					Jobs struct {
						Count  model.NoValNumber `json:"count"`
						Active struct {
							Per perIdentity `json:"per"`
						} `json:"active_jobs"`
						Per perIdentity `json:"per"`
					} `json:"jobs"`
					WallClock struct {
						Per struct {
							Job model.NoValNumber `json:"job"`
						} `json:"per"`
					} `json:"wall_clock"`
					TRES struct {
						Per struct {
							Job  []model.TRES `json:"job"`
							User []model.TRES `json:"user"`
						} `json:"per"`
					} `json:"tres"`
				} `json:"max"`
			} `json:"limits"`
		} `json:"qos"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return QOSResult{}, fmt.Errorf("decode QOS JSON: %w", err)
	}
	if err := response.validate(); err != nil {
		return QOSResult{}, err
	}
	result := QOSResult{QOS: []model.QOS{}, Version: response.Meta.Slurm.Version, Warnings: response.warnings()}
	for _, row := range response.QOS {
		max := row.Limits.Max
		result.QOS = append(result.QOS, model.QOS{Name: row.Name, Priority: row.Priority, UsageFactor: row.UsageFactor,
			MaxWall: max.WallClock.Per.Job, MaxJobs: max.Jobs.Count, MaxJobsPerUser: max.Jobs.Active.Per.User,
			MaxJobsPerAccount: max.Jobs.Active.Per.Account, MaxSubmitPerUser: max.Jobs.Per.User, MaxSubmitPerAccount: max.Jobs.Per.Account,
			MaxTRESPerJob: max.TRES.Per.Job, MaxTRESPerUser: max.TRES.Per.User})
	}
	return result, nil
}
