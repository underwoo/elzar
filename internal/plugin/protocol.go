package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"
)

const ProtocolVersion = 1

var ErrDisabled = errors.New("plugin disabled for this session")

type Column struct {
	ID     string   `json:"id"`
	Header string   `json:"header"`
	Width  int      `json:"width"`
	Tabs   []string `json:"tabs"`
}
type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
type Description struct {
	Protocol int       `json:"protocol"`
	Columns  []Column  `json:"columns"`
	Sections []Section `json:"sections"`
}
type ColumnValues map[string]map[string]string
type DetailSections map[string][]string

// Session owns one exec plugin and its circuit breaker. Calls are serialized.
// Construct once at load, call Columns once per successful refresh per tab, and
// call Detail when opening a job. Never invoke these methods from rendering.
type Session struct {
	mu          sync.Mutex
	name        string
	argv        []string
	provides    []string
	description Description
	executor    Executor
	timeout     time.Duration
	failures    int
	disabled    bool
	log         func(error)
}

// LoadExec renders static argv and runs describe exactly once. Unknown protocol
// versions and invalid descriptions reject the plugin without creating a session.
// log receives only the transition to disabled, once after three failures.
func LoadExec(ctx context.Context, p Plugin, executor Executor, options Options, log func(error)) (*Session, error) {
	if p.Type != "exec" {
		return nil, fmt.Errorf("plugin %s is not an exec plugin", p.Name)
	}
	name, args, err := Render(p.Manifest, Fields{})
	if err != nil {
		return nil, err
	}
	if executor == nil {
		executor = ProcessExecutor{}
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	session := &Session{name: p.Name, argv: append([]string{name}, args...), provides: slices.Clone(p.Provides), executor: executor, timeout: timeout, log: log}
	var description Description
	if err = session.execute(ctx, append(slices.Clone(session.argv), "describe"), nil, &description); err != nil {
		return nil, err
	}
	if err = validateDescription(description, p.Provides); err != nil {
		return nil, fmt.Errorf("plugin %s describe: %w", p.Name, err)
	}
	session.description = description
	return session, nil
}

// Description returns a copy, so consumers cannot change protocol validation.
func (s *Session) Description() Description {
	d := s.description
	d.Columns = slices.Clone(d.Columns)
	for i := range d.Columns {
		d.Columns[i].Tabs = slices.Clone(d.Columns[i].Tabs)
	}
	d.Sections = slices.Clone(d.Sections)
	return d
}
func (s *Session) Disabled() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.disabled }

func (s *Session) Columns(ctx context.Context, tab string, jobs []Fields) (ColumnValues, error) {
	if !slices.Contains(s.provides, "columns") {
		return nil, fmt.Errorf("plugin %s does not provide columns", s.name)
	}
	if tab != "queue" && tab != "recent" {
		return nil, fmt.Errorf("invalid plugin tab %q", tab)
	}
	if jobs == nil {
		jobs = []Fields{}
	}
	request := struct {
		Protocol int      `json:"protocol"`
		Op       string   `json:"op"`
		Tab      string   `json:"tab"`
		Jobs     []Fields `json:"jobs"`
	}{ProtocolVersion, "columns", tab, jobs}
	var response struct {
		Values ColumnValues `json:"values"`
	}
	err := s.call(ctx, request, &response, func() error {
		if response.Values == nil {
			return fmt.Errorf("columns response requires values object")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response.Values, nil
}

// Detail flattens the named fields beside raw, as required by protocol v1.
// raw must encode a JSON object; nil becomes an empty object.
func (s *Session) Detail(ctx context.Context, fields Fields, raw json.RawMessage) (DetailSections, error) {
	if !slices.Contains(s.provides, "detail") {
		return nil, fmt.Errorf("plugin %s does not provide detail", s.name)
	}
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("detail raw must be a JSON object")
	}
	job := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		job[key] = value
	}
	job["raw"] = raw
	request := struct {
		Protocol int            `json:"protocol"`
		Op       string         `json:"op"`
		Job      map[string]any `json:"job"`
	}{ProtocolVersion, "detail", job}
	var response struct {
		Sections DetailSections `json:"sections"`
	}
	err := s.call(ctx, request, &response, func() error {
		if response.Sections == nil {
			return fmt.Errorf("detail response requires sections object")
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response.Sections, nil
}

func (s *Session) call(ctx context.Context, request, response any, validate func() error) error {
	input, err := json.Marshal(request)
	if err != nil {
		return err
	}
	s.mu.Lock()
	var notification error
	defer func() {
		s.mu.Unlock()
		if notification != nil && s.log != nil {
			s.log(notification)
		}
	}()
	if s.disabled {
		return ErrDisabled
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	err = s.execute(ctx, s.argv, input, response)
	if err == nil {
		err = validate()
	}
	if err == nil {
		s.failures = 0
		return nil
	}
	// Navigation/shutdown cancellation is not a plugin failure. Timeouts are.
	if errors.Is(err, context.Canceled) {
		return err
	}
	s.failures++
	if s.failures >= 3 {
		s.disabled = true
		notification = fmt.Errorf("plugin %s disabled after 3 consecutive failures: %w", s.name, err)
	}
	return err
}

func (s *Session) execute(ctx context.Context, argv []string, input []byte, response any) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	output, stderr, err := s.executor.Exec(ctx, slices.Clone(argv), input)
	if len(output) > MaxOutputBytes {
		err = ErrOutputLimit
	}
	if err == nil && ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		if len(stderr) > MaxOutputBytes {
			stderr = stderr[:MaxOutputBytes]
		}
		return fmt.Errorf("plugin %s: %w; stderr: %s", s.name, err, strings.TrimSpace(string(stderr)))
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err = decoder.Decode(response); err != nil {
		return fmt.Errorf("plugin %s invalid JSON: %w", s.name, err)
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return fmt.Errorf("plugin %s: expected one JSON response", s.name)
	}
	return nil
}

func validateDescription(d Description, provides []string) error {
	if d.Protocol != ProtocolVersion {
		return fmt.Errorf("unsupported protocol %d (want %d)", d.Protocol, ProtocolVersion)
	}
	if len(d.Columns) > 0 && !slices.Contains(provides, "columns") {
		return fmt.Errorf("columns not declared in provides")
	}
	if len(d.Sections) > 0 && !slices.Contains(provides, "detail") {
		return fmt.Errorf("detail not declared in provides")
	}
	seen := map[string]bool{}
	for _, column := range d.Columns {
		if strings.TrimSpace(column.ID) == "" || strings.TrimSpace(column.Header) == "" || column.Width <= 0 || len(column.Tabs) == 0 {
			return fmt.Errorf("invalid column definition %q", column.ID)
		}
		if seen[column.ID] {
			return fmt.Errorf("duplicate column %q", column.ID)
		}
		seen[column.ID] = true
		tabs := map[string]bool{}
		for _, tab := range column.Tabs {
			if (tab != "queue" && tab != "recent") || tabs[tab] {
				return fmt.Errorf("invalid or duplicate column tab %q", tab)
			}
			tabs[tab] = true
		}
	}
	seen = map[string]bool{}
	for _, section := range d.Sections {
		if strings.TrimSpace(section.ID) == "" || strings.TrimSpace(section.Title) == "" {
			return fmt.Errorf("invalid section definition %q", section.ID)
		}
		if seen[section.ID] {
			return fmt.Errorf("duplicate section %q", section.ID)
		}
		seen[section.ID] = true
	}
	return nil
}
