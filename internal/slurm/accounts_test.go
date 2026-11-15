package slurm

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

type accountRunner struct {
	data  []byte
	err   error
	calls []string
}

func (r *accountRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, commandKey(name, args))
	return r.data, r.err
}
func accountFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/accounts/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestSharesFixtures(t *testing.T) {
	for _, name := range []string{"site-01_shares", "site-02_shares", "site-03_shares", "shares_all"} {
		t.Run(name, func(t *testing.T) {
			r := &accountRunner{data: accountFixture(t, name)}
			got, err := ListShares(context.Background(), r)
			if err != nil || len(got.Errors) != 0 || len(got.Shares) == 0 {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if !reflect.DeepEqual(r.calls, []string{"sshare --json -l -U -M all"}) {
				t.Fatal(r.calls)
			}
			clusters := map[string]bool{}
			user := false
			account := false
			for _, row := range got.Shares {
				clusters[row.Cluster] = true
				if row.Type == "USER" {
					user = true
					if row.User == "" || row.Account != row.Parent {
						t.Fatalf("bad user %+v", row)
					}
				}
				if row.Type == "ASSOCIATION" {
					account = true
					if row.Account == "" || row.User != "" {
						t.Fatalf("bad account %+v", row)
					}
				}
			}
			if !user || !account {
				t.Fatal("missing user/account rows")
			}
			if name == "shares_all" && len(clusters) != 4 {
				t.Fatalf("clusters: %v", clusters)
			}
		})
	}
}
func TestSharesFailures(t *testing.T) {
	good := string(accountFixture(t, "site-01_shares"))
	empty := `{"shares":{"shares":[]},"meta":{"slurm":{"version":"25.11.8"}}}`
	cases := []struct {
		name, data  string
		runErr      error
		fatal       bool
		diagnostics int
		rows        bool
	}{
		{"empty response", "", nil, true, 0, false},
		{"empty shares", empty, nil, false, 0, false},
		{"missing command", "", os.ErrNotExist, true, 0, false},
		{"timeout", "", context.DeadlineExceeded, true, 0, false},
		{"malformed", "{", nil, false, 1, false},
		{"old version", strings.Replace(empty, "25.11.8", "24.11.0", 1), nil, false, 1, false},
		{"missing version", `{"shares":{"shares":[]}}`, nil, false, 1, false},
		{"error envelope", strings.Replace(empty, `"shares":{`, `"errors":[{"description":"unavailable"}],"shares":{`, 1), nil, false, 1, false},
		{"partial", "CLUSTER: good\n" + good + "\nCLUSTER: retired\nunavailable\nCLUSTER: other\n" + good, nil, false, 1, true},
		{"exit with stdout", "CLUSTER: good\n" + good, errors.New("retired cluster unavailable"), false, 1, true},
		{"empty block", "CLUSTER: empty\nCLUSTER: good\n" + good, nil, false, 1, true},
		{"trailing JSON", good + empty, nil, false, 1, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ListShares(context.Background(), &accountRunner{data: []byte(tt.data), err: tt.runErr})
			if (err != nil) != tt.fatal || len(got.Errors) != tt.diagnostics || (len(got.Shares) > 0) != tt.rows {
				t.Fatalf("result=%+v err=%v", got, err)
			}
			if tt.name == "partial" {
				if got.Errors[0].Cluster != "retired" {
					t.Fatal(got.Errors)
				}
				seen := map[string]bool{}
				for _, row := range got.Shares {
					seen[row.Cluster] = true
				}
				if !seen["good"] || !seen["other"] {
					t.Fatal(seen)
				}
			}
		})
	}
}
func TestAccountFixtures(t *testing.T) {
	for _, site := range []string{"site-01", "site-02", "site-03"} {
		t.Run(site, func(t *testing.T) {
			r := &accountRunner{data: accountFixture(t, site+"_associations")}
			got, err := ListAssociations(context.Background(), r, "user-01")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r.calls, []string{"sacctmgr --json show assoc user=user-01"}) {
				t.Fatal(r.calls)
			}
			if site == "site-01" {
				if len(got.Associations) != 16 {
					t.Fatal(len(got.Associations))
				}
				for _, a := range got.Associations {
					if a.Account == "" || a.Cluster == "" || a.DefaultQOS == "" || len(a.QOS) == 0 {
						t.Fatalf("bad association %+v", a)
					}
				}
			} else if len(got.Associations) != 0 {
				t.Fatal("expected empty associations")
			}
			r = &accountRunner{data: accountFixture(t, site+"_qos")}
			qos, err := ListQOS(context.Background(), r)
			if err != nil || len(qos.QOS) == 0 {
				t.Fatalf("qos=%+v err=%v", qos, err)
			}
			if !reflect.DeepEqual(r.calls, []string{"sacctmgr --json show qos"}) {
				t.Fatal(r.calls)
			}
			if site == "site-03" && qos.Version.Major != 25 {
				t.Fatal(qos.Version)
			}
		})
	}
}
func TestAccountQueryErrors(t *testing.T) {
	for _, query := range []string{"assoc", "qos"} {
		for _, data := range []string{"", "{", `{"meta":{"slurm":{"version":"24.11"}}}`, `{"meta":{"slurm":{"version":"25.11"}},"errors":[{"description":"denied"}]}`, `{}`} {
			r := &accountRunner{data: []byte(data)}
			var err error
			if query == "assoc" {
				_, err = ListAssociations(context.Background(), r, "me")
			} else {
				_, err = ListQOS(context.Background(), r)
			}
			if err == nil {
				t.Errorf("%s accepted %s", query, data)
			}
		}
		r := &accountRunner{err: context.DeadlineExceeded}
		var err error
		if query == "assoc" {
			_, err = ListAssociations(context.Background(), r, "me")
		} else {
			_, err = ListQOS(context.Background(), r)
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	}
	r := &accountRunner{}
	if _, err := ListAssociations(context.Background(), r, ""); err == nil || len(r.calls) > 0 {
		t.Fatal("empty username invoked command")
	}
}
func TestExecRunnerRetainsPartialOutput(t *testing.T) {
	r := ExecRunner{Timeout: time.Second}
	data, err := r.Run(context.Background(), "sh", "-c", "printf partial; printf unavailable >&2; exit 1")
	if string(data) != "partial" || err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("data=%q err=%v", data, err)
	}
}
func TestSharesWarningsAndValues(t *testing.T) {
	data := `{"meta":{"slurm":{"version":"25.11.8","cluster":"local"}},"warnings":[{"description":"limited data"}],"shares":{"shares":[{"name":"a","type":["ASSOCIATION"],"shares":{"set":true,"number":10},"shares_normalized":{"set":true,"number":0.25},"usage":9007199254740993,"effective_usage":{"set":false},"fairshare":{"factor":{"set":true,"number":0.3},"level":{"infinite":true}}}]}}`
	got, err := ListShares(context.Background(), &accountRunner{data: []byte(data)})
	if err != nil || len(got.Shares) != 1 || len(got.Warnings) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	row := got.Shares[0]
	if row.Cluster != "local" || row.RawShares.Number != 10 || row.NormShares.Number != 0.25 || row.RawUsage != 9007199254740993 || row.EffectvUsage.Set || !row.Level.Infinite || row.FairShare.Number != 0.3 {
		t.Fatalf("%+v", row)
	}
}

func TestQOSLimits(t *testing.T) {
	data := `{"meta":{"slurm":{"version":"26.05.4"}},"warnings":[{"description":"partial visibility"}],"qos":[{"name":"q","priority":{"set":true,"number":9},"usage_factor":{"set":true,"number":0.5},"limits":{"max":{"jobs":{"count":{"set":true,"number":100},"active_jobs":{"per":{"account":{"set":true,"number":20},"user":{"set":true,"number":10}}},"per":{"account":{"set":true,"number":40},"user":{"set":true,"number":30}}},"wall_clock":{"per":{"job":{"set":true,"number":60}}},"tres":{"per":{"job":[{"type":"cpu","count":8}],"user":[{"type":"gres","name":"gpu","count":2}]}}}}}]}`
	got, err := ListQOS(context.Background(), &accountRunner{data: []byte(data)})
	if err != nil || len(got.QOS) != 1 || len(got.Warnings) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	q := got.QOS[0]
	if q.Priority.Number != 9 || q.UsageFactor.Number != 0.5 || q.MaxWall.Number != 60 || q.MaxJobs.Number != 100 || q.MaxJobsPerUser.Number != 10 || q.MaxJobsPerAccount.Number != 20 || q.MaxSubmitPerUser.Number != 30 || q.MaxSubmitPerAccount.Number != 40 || len(q.MaxTRESPerJob) != 1 || q.MaxTRESPerJob[0].Count != 8 || len(q.MaxTRESPerUser) != 1 || q.MaxTRESPerUser[0].Name != "gpu" {
		t.Fatalf("%+v", q)
	}
}

func TestAssociationIdentityAndDefault(t *testing.T) {
	data := `{"meta":{"slurm":{"version":"25.11.8"}},"associations":[{"cluster":"c","account":"a","user":"u","parent_account":"root","lineage":"/root/a/","qos":["normal"],"default":{"qos":"normal"},"is_default":true}]}`
	got, err := ListAssociations(context.Background(), &accountRunner{data: []byte(data)}, "u")
	if err != nil || len(got.Associations) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	a := got.Associations[0]
	if !a.IsDefault || a.Cluster != "c" || a.Account != "a" || a.User != "u" || a.Parent != "root" || a.Lineage != "/root/a/" || a.DefaultQOS != "normal" {
		t.Fatalf("%+v", a)
	}
}
