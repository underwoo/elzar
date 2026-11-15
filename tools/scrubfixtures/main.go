// Command scrubfixtures creates deterministic, anonymized Slurm JSON fixtures.
// Raw cluster dumps must stay outside the repository.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	input := flag.String("input", "", "raw Slurm JSON input file")
	output := flag.String("output", "", "anonymized JSON output file")
	inputDir := flag.String("input-dir", "", "directory of raw command-output samples (uses one shared anonymization map)")
	outputDir := flag.String("output-dir", "", "directory for scrubbed command-output fixtures")
	maxJobs := flag.Int("max-jobs", 0, "maximum number of jobs to retain (0 keeps all)")
	offset := flag.Int("offset", 0, "number of selected jobs to skip before writing")
	count := flag.Int("count", 0, "maximum jobs to write (0 writes all remaining jobs)")
	compact := flag.Bool("compact", false, "write compact JSON")
	omitJobResources := flag.Bool("omit-job-resources", false, "omit the large job_resources field from jobs")
	flag.Parse()
	if *inputDir != "" || *outputDir != "" {
		if *inputDir == "" || *outputDir == "" || *input != "" || *output != "" {
			fmt.Fprintln(os.Stderr, "use either -input/-output or -input-dir/-output-dir")
			os.Exit(2)
		}
		if err := scrubDirectory(*inputDir, *outputDir); err != nil {
			fmt.Fprintln(os.Stderr, "scrubfixtures:", err)
			os.Exit(1)
		}
		return
	}
	if *input == "" || *output == "" {
		fmt.Fprintln(os.Stderr, "usage: scrubfixtures -input raw.json -output testdata/fixture.json [-max-jobs N]\n       scrubfixtures -input-dir /private/samples -output-dir testdata/accounts")
		os.Exit(2)
	}
	if *maxJobs < 0 || *offset < 0 || *count < 0 {
		fmt.Fprintln(os.Stderr, "max-jobs, offset, and count must be zero or greater")
		os.Exit(2)
	}
	if err := scrubFile(*input, *output, *maxJobs, *offset, *count, *compact, *omitJobResources); err != nil {
		fmt.Fprintln(os.Stderr, "scrubfixtures:", err)
		os.Exit(1)
	}
}

func scrubFile(inputPath, outputPath string, maxJobs, offset, count int, compact, omitJobResources bool) error {
	input, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	decoder := json.NewDecoder(input)
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("decode input: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("input contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing input: %w", err)
	}
	cleaner := newScrubber()
	value = cleaner.cleanRoot(value, maxJobs)
	if omitJobResources {
		value = withoutJobResources(value)
	}
	value = sliceJobs(value, offset, count)
	if err := os.MkdirAll(dirName(outputPath), 0o755); err != nil {
		return err
	}
	output, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	if !compact {
		encoder.SetIndent("", "  ")
	}
	encodeErr := encoder.Encode(value)
	closeErr := output.Close()
	if encodeErr != nil {
		return encodeErr
	}
	return closeErr
}

// scrubDirectory processes each captured command output with one scrubber so
// identities (including cluster/account names) remain consistent across the
// resulting fixtures. Input files are expected to be the labelled .out format
// used by the M3 samples, or the CLUSTER-header format emitted by sshare -M.
func scrubDirectory(inputDir, outputDir string) error {
	entries, err := os.ReadDir(inputDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return err
	}
	cleaner := newScrubber()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".out") {
			continue
		}
		path := filepath.Join(inputDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(strings.ToLower(entry.Name()), "sshare-all") {
			out, err := scrubClusterDocuments(data, cleaner)
			if err != nil {
				return fmt.Errorf("%s: %w", entry.Name(), err)
			}
			if err := os.WriteFile(filepath.Join(outputDir, "shares_all.json"), out, 0o644); err != nil {
				return err
			}
			continue
		}
		documents, err := labelledDocuments(data)
		if err != nil {
			return fmt.Errorf("%s: %w", entry.Name(), err)
		}
		for _, document := range documents {
			if len(document.data) == 0 {
				continue
			}
			value, err := decodeSingleJSON(document.data)
			if err != nil {
				return fmt.Errorf("%s (%s): %w", entry.Name(), document.name, err)
			}
			value = cleaner.cleanAccountRoot(value)
			var output bytes.Buffer
			encoder := json.NewEncoder(&output)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(value); err != nil {
				return err
			}
			base := strings.TrimSuffix(entry.Name(), ".out")
			if err := os.WriteFile(filepath.Join(outputDir, base+"_"+document.name+".json"), output.Bytes(), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

type labelledDocument struct {
	name string
	data []byte
}

// labelledDocuments extracts JSON following the captured command labels and
// deliberately discards the labels, which may contain the real username.
func labelledDocuments(data []byte) ([]labelledDocument, error) {
	var docs []labelledDocument
	for len(bytes.TrimSpace(data)) > 0 {
		end := bytes.IndexByte(data, '\n')
		if end < 0 {
			break
		}
		line := strings.TrimSpace(string(data[:end]))
		data = data[end+1:]
		name := ""
		switch {
		case strings.HasPrefix(line, "sshare "):
			name = "shares"
		case strings.HasPrefix(line, "sacctmgr ") && strings.Contains(line, "show assoc"):
			name = "associations"
		case strings.HasPrefix(line, "sacctmgr ") && strings.Contains(line, "show qos"):
			name = "qos"
		case strings.HasPrefix(line, "scontrol ") && strings.Contains(line, "show partition"):
			name = "partitions"
		}
		if name == "" {
			continue
		}
		data = bytes.TrimSpace(data)
		_, n, err := decodeJSONPrefix(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		// Partition fixtures belong to the later partition-access change.
		if name != "partitions" {
			docs = append(docs, labelledDocument{name: name, data: data[:n]})
		}
		data = data[n:]
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no labelled JSON documents")
	}
	return docs, nil
}

func decodeSingleJSON(data []byte) (any, error) {
	value, consumed, err := decodeJSONPrefix(data)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data[consumed:])) != 0 {
		return nil, fmt.Errorf("trailing data after JSON document")
	}
	return value, nil
}

func decodeJSONPrefix(data []byte) (any, int, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, 0, err
	}
	return value, int(decoder.InputOffset()), nil
}

// scrubClusterDocuments preserves sshare's CLUSTER headers while scrubbing
// each following JSON document with the same mapping used for other samples.
func scrubClusterDocuments(data []byte, cleaner *scrubber) ([]byte, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	var output bytes.Buffer
	var currentCluster string
	var jsonLines bytes.Buffer
	flush := func() error {
		if jsonLines.Len() == 0 {
			return nil
		}
		value, err := decodeSingleJSON(jsonLines.Bytes())
		if err != nil {
			return err
		}
		cleanedCluster := cleaner.fake("cluster", currentCluster, "cluster")
		value = cleaner.cleanAccountRoot(value)
		fmt.Fprintf(&output, "CLUSTER: %s\n", cleanedCluster)
		encoder := json.NewEncoder(&output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(value); err != nil {
			return err
		}
		jsonLines.Reset()
		return nil
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(strings.ToUpper(trimmed), "CLUSTER:") {
			if err := flush(); err != nil {
				return nil, err
			}
			currentCluster = strings.TrimSpace(trimmed[len("CLUSTER:"):])
			continue
		}
		if jsonLines.Len() != 0 || strings.HasPrefix(trimmed, "{") {
			jsonLines.Write(line)
			jsonLines.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func withoutJobResources(value any) any {
	root, ok := value.(map[string]any)
	if !ok {
		return value
	}
	jobs, ok := root["jobs"].([]any)
	if !ok {
		return value
	}
	for _, raw := range jobs {
		if job, ok := raw.(map[string]any); ok {
			delete(job, "job_resources")
		}
	}
	return root
}

func dirName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		if i == 0 {
			return "/"
		}
		return path[:i]
	}
	return "."
}

type scrubber struct {
	maps map[string]map[string]string
	seq  map[string]int
}

func newScrubber() *scrubber {
	return &scrubber{maps: make(map[string]map[string]string), seq: make(map[string]int)}
}

func (s *scrubber) cleanRoot(value any, maxJobs int) any {
	root, ok := value.(map[string]any)
	if !ok {
		return s.clean("", value)
	}
	if jobs, ok := root["jobs"].([]any); ok && maxJobs > 0 && len(jobs) > maxJobs {
		root["jobs"] = prioritizePending(jobs, maxJobs)
	}
	return s.cleanMap(root)
}

// Put pending examples first when trimming, then fill the limit in input order.
func prioritizePending(jobs []any, limit int) []any {
	pendingJobs := make([]any, 0, limit)
	others := make([]any, 0, len(jobs))
	for _, raw := range jobs {
		job, _ := raw.(map[string]any)
		states, _ := job["job_state"].([]any)
		isPending := false
		for _, state := range states {
			if strings.EqualFold(fmt.Sprint(state), "PENDING") {
				isPending = true
				break
			}
		}
		if isPending {
			pendingJobs = append(pendingJobs, raw)
		} else {
			others = append(others, raw)
		}
	}
	selected := append(pendingJobs, others...)
	if len(selected) > limit {
		selected = selected[:limit]
	}
	return selected
}

func sliceJobs(value any, offset, count int) any {
	root, ok := value.(map[string]any)
	if !ok {
		return value
	}
	jobs, ok := root["jobs"].([]any)
	if !ok {
		return value
	}
	if offset >= len(jobs) {
		root["jobs"] = []any{}
		return root
	}
	end := len(jobs)
	if count > 0 && offset+count < end {
		end = offset + count
	}
	root["jobs"] = jobs[offset:end]
	return root
}

func (s *scrubber) clean(key string, value any) any {
	return s.cleanAt(key, "", value)
}

func (s *scrubber) cleanAt(key, parent string, value any) any {
	if value == nil {
		return nil
	}
	normalized := strings.ToLower(key)
	switch normalized {
	case "comment":
		return s.scrubComment(value)
	case "lineage":
		return s.scrubLineageValue(value)
	case "admin_comment", "system_comment", "job_comment", "extra":
		return scrubStrings(value, "")
	case "script":
		if _, ok := value.(string); ok {
			return "#!/bin/sh\n# scrubbed job script\n"
		}
		return scrubStrings(value, "[scrubbed script]")
	}
	if replacement, ok := s.replacement(normalized, parent, value); ok {
		return replacement
	}
	switch value := value.(type) {
	case map[string]any:
		return s.cleanMap(value, normalized)
	case []any:
		cleaned := make([]any, len(value))
		for i, item := range value {
			cleaned[i] = s.cleanAt(key, parent, item)
		}
		return cleaned
	default:
		return value
	}
}

func (s *scrubber) scrubComment(value any) any {
	switch comment := value.(type) {
	case string:
		return s.fake("comment", comment, "comment")
	case map[string]any:
		cleaned := make(map[string]any, len(comment))
		for key, item := range comment {
			if strings.EqualFold(key, "job") {
				cleaned[key] = s.scrubComment(item)
			} else {
				cleaned[key] = scrubStrings(item, "")
			}
		}
		return cleaned
	default:
		return scrubStrings(value, "")
	}
}

func (s *scrubber) scrubLineageValue(value any) any {
	switch lineage := value.(type) {
	case string:
		var out strings.Builder
		start := 0
		for i := 0; i <= len(lineage); i++ {
			if i != len(lineage) && lineage[i] != '/' && lineage[i] != ':' {
				continue
			}
			if part := lineage[start:i]; part != "" {
				out.WriteString(s.fake("account", part, "account"))
			}
			if i < len(lineage) {
				out.WriteByte(lineage[i])
			}
			start = i + 1
		}
		return out.String()
	case []any:
		cleaned := make([]any, len(lineage))
		for i, item := range lineage {
			cleaned[i] = s.scrubLineageValue(item)
		}
		return cleaned
	default:
		return value
	}
}

func (s *scrubber) cleanMap(value map[string]any, parent ...string) map[string]any {
	parentKey := ""
	if len(parent) != 0 {
		parentKey = parent[0]
	}
	cleaned := make(map[string]any, len(value))
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := value[key]
		if parentKey == "shares" && key == "name" {
			types, _ := value["type"].([]any)
			if len(types) > 0 && types[0] == "USER" {
				text, _ := item.(string)
				cleaned[key] = s.fake("user", text, "user")
				continue
			}
		}
		cleaned[key] = s.cleanAt(key, parentKey, item)
	}
	return cleaned
}

func scrubStrings(value any, replacement string) any {
	switch value := value.(type) {
	case string:
		return replacement
	case map[string]any:
		cleaned := make(map[string]any, len(value))
		for key, item := range value {
			cleaned[key] = scrubStrings(item, replacement)
		}
		return cleaned
	case []any:
		cleaned := make([]any, len(value))
		for i, item := range value {
			cleaned[i] = scrubStrings(item, replacement)
		}
		return cleaned
	default:
		return value
	}
}

func (s *scrubber) replacement(key, parent string, value any) (any, bool) {
	if key == "user_id" || key == "group_id" || key == "association_id" || (key == "id" && (parent == "association" || parent == "associations" || parent == "shares")) {
		base := 10000
		switch key {
		case "group_id":
			base = 20000
		case "association_id", "id":
			base = 30000
		}
		return s.fakeNumber(key, fmt.Sprint(value), base), true
	}
	text, isText := value.(string)
	if !isText {
		return nil, false
	}
	switch key {
	case "user_name", "user", "mail_user", "username", "kill_request_user":
		return s.fake("user", text, "user"), true
	case "group_name", "group":
		return s.fake("group", text, "group"), true
	case "account":
		return s.fake("account", text, "account"), true
	case "parent", "parent_account":
		if key == "parent_account" || parent == "shares" || parent == "associations" {
			return s.fake("account", text, "account"), true
		}
	case "association_id":
		return s.fakeNumber("association_id", text, 30000), true
	case "wckey":
		return s.fake(key, text, "wckey"), true
	case "name":
		switch parent {
		case "jobs", "reservation":
			return s.fake("name", text, "job"), true
		case "shares", "associations":
			return s.fake("account", text, "account"), true
		case "qos":
			return s.fake("qos", text, "qos"), true
		case "partitions":
			return s.fake("partition", text, "partition"), true
		default:
			return nil, false
		}
	case "partition":
		return s.fake(key, text, "partition"), true
	case "qos", "qosreq":
		return s.fake(key, text, "qos"), true
	case "cluster", "federation_origin":
		return s.fake("cluster", text, "cluster"), true
	case "current_working_directory", "working_directory", "cwd", "stdin", "stdout", "stderr", "standard_input", "standard_output", "standard_error", "stdin_expanded", "stdout_expanded", "stderr_expanded":
		return s.fakePath(key, text), true
	case "source":
		if parent == "client" {
			return s.fakePath(key, text), true
		}
	case "range":
		if parent == "nodes" {
			return s.fake("node", text, "node"), true
		}
	case "node":
		return s.fake("node", text, "node"), true
	case "sluid", "original_sluid":
		return s.fake("sluid", text, "sluid"), true
	case "command", "submit_line":
		return "./job.sh", true
	case "nodes", "node_list", "list", "node_name", "required_nodes", "scheduled_nodes", "allocating_node", "batch_host", "failed_node", "allocation_nodes":
		return s.fake("node", text, "node"), true
	}
	return nil, false
}

func (s *scrubber) fake(kind, original, prefix string) string {
	if original == "" {
		return original
	}
	if s.maps[kind] == nil {
		s.maps[kind] = make(map[string]string)
	}
	if value, ok := s.maps[kind][original]; ok {
		return value
	}
	s.seq[kind]++
	value := fmt.Sprintf("%s-%02d", prefix, s.seq[kind])
	s.maps[kind][original] = value
	return value
}

func (s *scrubber) fakeNumber(kind, original string, base int) any {
	if s.maps[kind] == nil {
		s.maps[kind] = make(map[string]string)
	}
	if value, ok := s.maps[kind][original]; ok {
		n, _ := strconv.Atoi(value)
		return n
	}
	s.seq[kind]++
	n := base + s.seq[kind]
	s.maps[kind][original] = strconv.Itoa(n)
	return n
}

func (s *scrubber) fakePath(key, original string) string {
	if original == "" {
		return ""
	}
	return "/tmp/slurmtui-fixture/" + s.fake("path", original, "path")
}

// Account fixtures retain the fields exercised by this milestone, avoiding
// unrelated free-form fields and large TRES usage arrays in raw site samples.
func (s *scrubber) cleanAccountRoot(value any) any {
	root, ok := value.(map[string]any)
	if !ok {
		return s.cleanRoot(value, 0)
	}
	if envelope, ok := root["shares"].(map[string]any); ok {
		rows, _ := envelope["shares"].([]any)
		keep := make(map[string]bool)
		for i, raw := range rows {
			row, _ := raw.(map[string]any)
			types, _ := row["type"].([]any)
			if i < 4 || (len(types) > 0 && types[0] == "USER") {
				keep[fmt.Sprint(row["name"])] = true
				keep[fmt.Sprint(row["parent"])] = true
			}
		}
		for changed := true; changed; {
			changed = false
			for _, raw := range rows {
				row, _ := raw.(map[string]any)
				if keep[fmt.Sprint(row["name"])] && !keep[fmt.Sprint(row["parent"])] {
					keep[fmt.Sprint(row["parent"])] = true
					changed = true
				}
			}
		}
		trimmed := make([]any, 0)
		for _, raw := range rows {
			row, _ := raw.(map[string]any)
			if keep[fmt.Sprint(row["name"])] {
				delete(row, "tres")
				trimmed = append(trimmed, row)
			}
		}
		envelope["shares"] = trimmed
	}
	if rows, ok := root["qos"].([]any); ok {
		for _, raw := range rows {
			row, _ := raw.(map[string]any)
			delete(row, "description")
		}
	}
	return s.cleanRoot(root, 0)
}
