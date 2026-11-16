package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var testPluginPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "elzar-test-plugin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testPluginPath = filepath.Join(dir, "plugin")
	output, err := exec.Command("go", "build", "-o", testPluginPath, "./testdata/execplugin").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build test plugin: %v\n%s", err, output)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const descriptionJSON = `{"protocol":1,"columns":[{"id":"note","header":"Note","width":12,"tabs":["queue","recent"]}],"sections":[{"id":"extra","title":"Extra"}],"unknown":42}`

func execPlugin() Plugin {
	return Plugin{Manifest: Manifest{Name: "test", Description: "test protocol", Type: "exec", Command: testPluginPath, Provides: []string{"columns", "detail"}}}
}

type execCall struct {
	argv  []string
	stdin []byte
}
type execResponse struct {
	stdout, stderr string
	err            error
}
type FakeExecutor struct {
	calls     []execCall
	responses []execResponse
	run       func(context.Context, []string, []byte) ([]byte, []byte, error)
}

func (f *FakeExecutor) Exec(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, error) {
	f.calls = append(f.calls, execCall{append([]string(nil), argv...), append([]byte(nil), stdin...)})
	if f.run != nil {
		return f.run(ctx, argv, stdin)
	}
	if len(f.responses) == 0 {
		return nil, nil, fmt.Errorf("unexpected exec call")
	}
	r := f.responses[0]
	f.responses = f.responses[1:]
	return []byte(r.stdout), []byte(r.stderr), r.err
}
func loadedSession(t *testing.T, fake *FakeExecutor, log func(error)) *Session {
	t.Helper()
	fake.responses = append([]execResponse{{stdout: descriptionJSON}}, fake.responses...)
	session, err := LoadExec(context.Background(), execPlugin(), fake, DefaultOptions(), log)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestExecManifest(t *testing.T) {
	text := "name: test\ndescription: test plugin\ntype: exec\ncommand: echo\nprovides: [columns, detail]\n"
	if _, err := Parse([]byte(text)); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(text, "provides: [columns, detail]", "provides: []", 1),
		strings.Replace(text, "columns, detail", "columns, columns", 1),
		strings.Replace(text, "columns, detail", "unknown", 1),
		text + "key: z\n", text + "mode: background\n", text + "scopes: [queue]\n", text + "confirm: true\n",
		validManifest + "provides: [columns]\n",
	} {
		if _, err := Parse([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid manifest: %s", invalid)
		}
	}
}

func TestProtocolRequestsAndUnknownFields(t *testing.T) {
	fake := &FakeExecutor{responses: []execResponse{
		{stdout: `{"values":{"123":{"note":"hello"},"456":{"note":"world"}},"future":true}`},
		{stdout: `{"sections":{"extra":["line","two"]},"future":true}`},
	}}
	session := loadedSession(t, fake, nil)
	values, err := session.Columns(context.Background(), "queue", []Fields{{"JobID": "123", "Name": "a b"}, {"JobID": "456", "Name": "c"}})
	if err != nil || values["123"]["note"] != "hello" || values["456"]["note"] != "world" {
		t.Fatalf("values=%v err=%v", values, err)
	}
	sections, err := session.Detail(context.Background(), Fields{"JobID": "123", "Name": "a b"}, json.RawMessage(`{"custom":true}`))
	if err != nil || !reflect.DeepEqual(sections["extra"], []string{"line", "two"}) {
		t.Fatalf("sections=%v err=%v", sections, err)
	}
	if len(fake.calls) != 3 || !reflect.DeepEqual(fake.calls[0].argv, []string{testPluginPath, "describe"}) || len(fake.calls[0].stdin) != 0 {
		t.Fatalf("calls=%+v", fake.calls)
	}
	for _, call := range fake.calls[1:] {
		if !reflect.DeepEqual(call.argv, []string{testPluginPath}) {
			t.Fatalf("op leaked into argv: %v", call.argv)
		}
	}
	var columns struct {
		Protocol int
		Op, Tab  string
		Jobs     []Fields
	}
	if err = json.Unmarshal(fake.calls[1].stdin, &columns); err != nil {
		t.Fatal(err)
	}
	if columns.Protocol != 1 || columns.Op != "columns" || columns.Tab != "queue" || len(columns.Jobs) != 2 {
		t.Fatalf("request=%s", fake.calls[1].stdin)
	}
	var detail struct {
		Protocol int
		Op       string
		Job      map[string]json.RawMessage
	}
	if err = json.Unmarshal(fake.calls[2].stdin, &detail); err != nil {
		t.Fatal(err)
	}
	if detail.Op != "detail" || string(detail.Job["Name"]) != `"a b"` || string(detail.Job["raw"]) != `{"custom":true}` {
		t.Fatalf("request=%s", fake.calls[2].stdin)
	}
	// Returned metadata is not shared mutable session state.
	d := session.Description()
	d.Columns[0].Tabs[0] = "changed"
	if session.Description().Columns[0].Tabs[0] != "queue" {
		t.Fatal("description alias")
	}
}

func TestDescriptionRejection(t *testing.T) {
	for _, response := range []string{
		`{"protocol":2}`, `{}`, `null`, `{"protocol":1} {}`,
		`{"protocol":1,"columns":[{"id":"","header":"h","width":1,"tabs":["queue"]}]}`,
		`{"protocol":1,"columns":[{"id":"x","header":"h","width":0,"tabs":["queue"]}]}`,
		`{"protocol":1,"columns":[{"id":"x","header":"h","width":1,"tabs":["accounts"]}]}`,
		`{"protocol":1,"sections":[{"id":"x","title":""}]}`,
		`{"protocol":1,"sections":[{"id":"x","title":"x"},{"id":"x","title":"y"}]}`,
	} {
		fake := &FakeExecutor{responses: []execResponse{{stdout: response}}}
		if _, err := LoadExec(context.Background(), execPlugin(), fake, DefaultOptions(), nil); err == nil {
			t.Fatalf("accepted %s", response)
		}
	}
}

func TestBreakerConsecutiveFailuresAndOneLog(t *testing.T) {
	fake := &FakeExecutor{responses: []execResponse{
		{stdout: "{"}, {stdout: "{"}, {stdout: `{"values":{}}`},
		{stdout: "{"}, {stdout: "{}"}, {stderr: "boom", err: errors.New("exit status 1")},
	}}
	var logs []error
	var session *Session
	session = loadedSession(t, fake, func(err error) {
		logs = append(logs, err)
		if !session.Disabled() {
			t.Error("callback before disabled")
		}
	})
	for i := 0; i < 6; i++ {
		_, err := session.Columns(context.Background(), "queue", nil)
		if (i == 2) != (err == nil) {
			t.Fatalf("call %d err=%v", i, err)
		}
		if i < 5 && session.Disabled() {
			t.Fatalf("disabled early at %d", i)
		}
	}
	for i := 0; i < 4; i++ {
		if _, err := session.Columns(context.Background(), "queue", nil); !errors.Is(err, ErrDisabled) {
			t.Fatalf("disabled err=%v", err)
		}
	}
	if len(fake.calls) != 7 || len(logs) != 1 || !strings.Contains(logs[0].Error(), "boom") {
		t.Fatalf("calls=%d logs=%v", len(fake.calls), logs)
	}
}

func TestProtocolOutputLimitAndTimeout(t *testing.T) {
	fake := &FakeExecutor{responses: []execResponse{{stdout: strings.Repeat("x", MaxOutputBytes+1)}}}
	session := loadedSession(t, fake, nil)
	if _, err := session.Columns(context.Background(), "queue", nil); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("limit=%v", err)
	}
	fake.run = func(ctx context.Context, _ []string, _ []byte) ([]byte, []byte, error) {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	session.timeout = 10 * time.Millisecond
	if _, err := session.Columns(context.Background(), "queue", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout=%v", err)
	}
}

func TestRealExecProtocol(t *testing.T) {
	p := execPlugin()
	p.Args = []string{"ok"}
	session, err := LoadExec(context.Background(), p, nil, DefaultOptions(), nil)
	if err != nil {
		t.Fatal(err)
	}
	values, err := session.Columns(context.Background(), "recent", []Fields{{"JobID": "12", "Name": "a b"}, {"JobID": "13", "Name": "second"}})
	if err != nil || values["12"]["note"] != "recent:a b" || len(values) != 2 {
		t.Fatalf("values=%v err=%v", values, err)
	}
	sections, err := session.Detail(context.Background(), Fields{"Name": "a b"}, json.RawMessage(`{"custom":"raw value"}`))
	if err != nil || !reflect.DeepEqual(sections["extra"], []string{"a b", "raw value"}) {
		t.Fatalf("sections=%v err=%v", sections, err)
	}
}

func TestRealExecFailures(t *testing.T) {
	for _, mode := range []string{"timeout", "exit", "malformed", "oversized", "protocol", "describe-timeout", "stderr"} {
		t.Run(mode, func(t *testing.T) {
			p := execPlugin()
			p.Args = []string{mode}
			options := DefaultOptions()
			options.Timeout = 200 * time.Millisecond
			session, err := LoadExec(context.Background(), p, nil, options, nil)
			if mode == "protocol" || mode == "describe-timeout" {
				if err == nil {
					t.Fatal("invalid describe accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			_, err = session.Columns(context.Background(), "queue", nil)
			switch mode {
			case "timeout":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("err=%v", err)
				}
			case "exit":
				if err == nil || !strings.Contains(err.Error(), "intentional stderr") {
					t.Fatalf("err=%v", err)
				}
			case "malformed":
				if err == nil || !strings.Contains(err.Error(), "JSON") {
					t.Fatalf("err=%v", err)
				}
			case "oversized":
				if !errors.Is(err, ErrOutputLimit) {
					t.Fatalf("err=%v", err)
				}
			case "stderr":
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestConcurrentBreaker(t *testing.T) {
	fake := &FakeExecutor{}
	session := loadedSession(t, fake, nil)
	fake.run = func(context.Context, []string, []byte) ([]byte, []byte, error) { return nil, nil, errors.New("broken") }
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = session.Columns(context.Background(), "queue", nil) }()
	}
	wg.Wait()
	if len(fake.calls) != 4 || !session.Disabled() {
		t.Fatalf("calls=%d disabled=%v", len(fake.calls), session.Disabled())
	}
}

func TestInvalidRequestsAndCancellationDoNotTripBreaker(t *testing.T) {
	fake := &FakeExecutor{responses: []execResponse{{stdout: `{"values":{}}`}}}
	session := loadedSession(t, fake, nil)
	for i := 0; i < 4; i++ {
		if _, err := session.Columns(context.Background(), "accounts", nil); err == nil {
			t.Fatal("invalid tab accepted")
		}
		if _, err := session.Detail(context.Background(), Fields{}, json.RawMessage("null")); err == nil {
			t.Fatal("null raw accepted")
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := session.Columns(ctx, "queue", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel err=%v", err)
		}
	}
	if len(fake.calls) != 1 || session.Disabled() {
		t.Fatalf("invalid local request reached process or breaker: %d", len(fake.calls))
	}
	if _, err := session.Columns(context.Background(), "queue", nil); err != nil {
		t.Fatal(err)
	}
	var request struct{ Jobs []Fields }
	if err := json.Unmarshal(fake.calls[1].stdin, &request); err != nil || request.Jobs == nil {
		t.Fatalf("empty batch must be []: %s", fake.calls[1].stdin)
	}
}

func TestProcessOutputBoundaryAndStderrBound(t *testing.T) {
	var canceled bool
	writer := &boundedOutput{limit: 4, cancel: func() { canceled = true }}
	if n, err := writer.Write([]byte("1234")); n != 4 || err != nil || canceled {
		t.Fatalf("exact cap: %d %v", n, err)
	}
	if _, err := writer.Write([]byte("5")); !errors.Is(err, ErrOutputLimit) || !canceled || writer.buffer.String() != "1234" {
		t.Fatalf("overflow=%v", err)
	}
	stderr := &boundedOutput{limit: 4}
	if n, err := stderr.Write([]byte("123456")); n != 6 || err != nil || stderr.buffer.Len() != 4 {
		t.Fatalf("stderr: %d %v %d", n, err, stderr.buffer.Len())
	}
	if _, _, err := (ProcessExecutor{}).Exec(context.Background(), nil, nil); err == nil {
		t.Fatal("empty argv accepted")
	}
}

func TestExecDiscovery(t *testing.T) {
	options := discoveryOptions(t)
	manifest := "name: protocol\ndescription: protocol test\ntype: exec\ncommand: " + testPluginPath + "\nprovides: [columns, detail]\n"
	writeFile(t, filepath.Join(options.UserDir, "protocol.yaml"), manifest, 0o600)
	plugins, errs := Discover(options)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	found := false
	for _, p := range plugins {
		if p.Type == "exec" {
			found = true
			if _, err := LoadExec(context.Background(), p, nil, options, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found {
		t.Fatal("exec manifest not discovered")
	}
}
