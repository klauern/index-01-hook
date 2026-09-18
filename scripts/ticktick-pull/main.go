package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxBody = 4 << 20

// apiBase is a variable so tests can point the read-only client at a local server.
var apiBase = "https://api.ticktick.com"

type project struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Kind       string          `json:"kind"`
	Closed     bool            `json:"closed"`
	Permission json.RawMessage `json:"permission"`
}
type projectOut struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Closed   bool   `json:"closed"`
	Writable bool   `json:"writable"`
}
type task struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"projectId"`
	Title     string          `json:"title"`
	Content   string          `json:"content"`
	Kind      string          `json:"kind"`
	Status    int             `json:"status"`
	Priority  int             `json:"priority"`
	DueDate   string          `json:"dueDate"`
	Tags      []string        `json:"tags"`
	Items     json.RawMessage `json:"items,omitempty"`
}
type taskOut struct {
	ID          string          `json:"id"`
	ProjectID   string          `json:"project_id"`
	ProjectName string          `json:"project_name"`
	Title       string          `json:"title"`
	Content     string          `json:"content"`
	Kind        string          `json:"kind"`
	Status      int             `json:"status"`
	Priority    int             `json:"priority"`
	DueDate     string          `json:"due_date"`
	Tags        []string        `json:"tags"`
	Items       json.RawMessage `json:"items,omitempty"`
}
type snapshot struct {
	PulledAt string       `json:"pulled_at"`
	Projects []projectOut `json:"projects"`
	Tasks    []taskOut    `json:"tasks"`
}
type marker struct {
	Fingerprint string
	Index       int
}

var markerRE = regexp.MustCompile(`\[index01:([0-9a-f]{64}):([0-9]+)\]`)

func parseMarker(s string) (marker, bool) {
	m := markerRE.FindStringSubmatch(s)
	if m == nil {
		return marker{}, false
	}
	i, err := strconv.Atoi(m[2])
	if err != nil || i < 0 {
		return marker{}, false
	}
	return marker{m[1], i}, true
}
func allMarkers(s string) []marker {
	ms := markerRE.FindAllStringSubmatch(s, -1)
	out := make([]marker, 0, len(ms))
	for _, m := range ms {
		i, e := strconv.Atoi(m[2])
		if e == nil && i >= 0 {
			out = append(out, marker{m[1], i})
		}
	}
	return out
}

func writable(raw json.RawMessage) bool {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	var s string
	return json.Unmarshal(raw, &s) == nil && strings.EqualFold(s, "write")
}
func privatePath(path string) error {
	if strings.TrimSpace(path) == "" || path == "-" {
		return errors.New("output requires a private file path")
	}
	p, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	for {
		if st, e := os.Stat(filepath.Join(p, ".git")); e == nil && st.IsDir() {
			return fmt.Errorf("refusing path inside repository: %s", path)
		}
		n := filepath.Dir(p)
		if n == p {
			break
		}
		p = n
	}
	return nil
}
func write0600(path string, v any) error {
	if err := privatePath(path); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	return err
}

type puller struct {
	token string
	hc    *http.Client
}

func (p *puller) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, apiBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Accept", "application/json")
	res, err := p.hc.Do(req)
	if err != nil {
		return errors.New("TickTick request failed")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("TickTick GET %s returned HTTP %s", path, res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		return fmt.Errorf("TickTick GET %s response is too large or invalid", path)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if err = dec.Decode(out); err != nil {
		return fmt.Errorf("TickTick GET %s response is invalid", path)
	}
	var extra any
	if err = dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("TickTick GET %s response has trailing data", path)
	}
	return nil
}
func pull(out string, hc *http.Client) error {
	if err := privatePath(out); err != nil {
		return err
	}
	token := os.Getenv("INDEX01_TICKTICK_TOKEN")
	if token == "" {
		return errors.New("INDEX01_TICKTICK_TOKEN is required")
	}
	if hc == nil {
		hc = &http.Client{}
	}
	copyClient := *hc
	copyClient.Timeout = 30 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	p := &puller{token, &copyClient}
	var projects []project
	if err := p.get("/open/v1/project", &projects); err != nil {
		return err
	}
	s := snapshot{PulledAt: time.Now().UTC().Format(time.RFC3339), Projects: make([]projectOut, 0, len(projects)), Tasks: []taskOut{}}
	byID := map[string]string{}
	for _, x := range projects {
		s.Projects = append(s.Projects, projectOut{x.ID, x.Name, x.Kind, x.Closed, writable(x.Permission)})
		byID[x.ID] = x.Name
	}
	for _, x := range projects {
		var data struct {
			Tasks []task `json:"tasks"`
		}
		path := "/open/v1/project/" + url.PathEscape(x.ID) + "/data"
		if err := p.get(path, &data); err != nil {
			return err
		}
		for _, t := range data.Tasks {
			s.Tasks = append(s.Tasks, taskOut{t.ID, t.ProjectID, byID[t.ProjectID], t.Title, t.Content, t.Kind, t.Status, t.Priority, t.DueDate, t.Tags, t.Items})
		}
	}
	if err := write0600(out, s); err != nil {
		return err
	}
	markers := 0
	for _, t := range s.Tasks {
		if len(allMarkers(t.Content)) > 0 {
			markers++
		}
	}
	fmt.Printf("projects: %d\ntasks: %d\ntasks carrying index01 marker: %d\n", len(s.Projects), len(s.Tasks), markers)
	return nil
}

type evidence struct {
	Archive  []archive `json:"evidence_archive"`
	Findings []finding `json:"findings"`
}
type archive struct {
	Fingerprint string      `json:"recording_fingerprint"`
	Transcript  string      `json:"transcript"`
	Extraction  *extraction `json:"extraction"`
	Deliveries  []delivery  `json:"deliveries"`
}
type extraction struct {
	Items []realCandidate `json:"Items"`
}
type delivery struct {
	ItemIndex int    `json:"item_index"`
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Notes     string `json:"notes"`
}
type finding struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}
type realCandidate struct {
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Content      string   `json:"content"`
	Due          string   `json:"due"`
	AllDay       bool     `json:"all_day"`
	Priority     int      `json:"priority"`
	Tags         []string `json:"tags"`
	ProjectAlias string   `json:"project_alias"`
}

func (c *realCandidate) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	get := func(name string) json.RawMessage {
		for k, v := range raw {
			if strings.EqualFold(strings.ReplaceAll(k, "_", ""), strings.ReplaceAll(name, "_", "")) {
				return v
			}
		}
		return nil
	}
	str := func(name string, dst *string) error {
		v := get(name)
		if len(v) == 0 || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil
		}
		return json.Unmarshal(v, dst)
	}
	if err := str("kind", &c.Kind); err != nil {
		return err
	}
	if err := str("title", &c.Title); err != nil {
		return err
	}
	if err := str("content", &c.Content); err != nil {
		return err
	}
	if c.Content == "" {
		_ = str("notes", &c.Content)
	}
	if err := str("due", &c.Due); err != nil {
		return err
	}
	if err := str("project_alias", &c.ProjectAlias); err != nil {
		return err
	}
	if v := get("all_day"); len(v) > 0 && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		if err := json.Unmarshal(v, &c.AllDay); err != nil {
			return err
		}
	}
	if v := get("priority"); len(v) > 0 && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		if err := json.Unmarshal(v, &c.Priority); err != nil {
			return err
		}
	}
	if v := get("tags"); len(v) > 0 && !bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
		if err := json.Unmarshal(v, &c.Tags); err != nil {
			return err
		}
	}
	return nil
}

type caseOut struct {
	CaseID         string        `json:"case_id"`
	Split          string        `json:"split"`
	Transcript     string        `json:"transcript"`
	Candidate      realCandidate `json:"candidate"`
	ObservedStatus string        `json:"observed_status"`
	WeakLabel      string        `json:"weak_label"`
	WeakRouteOK    bool          `json:"weak_route_ok"`
}
type casesFile struct {
	BuiltAt string      `json:"built_at"`
	Cases   []caseOut   `json:"cases"`
	Summary caseSummary `json:"summary"`
}
type caseSummary struct {
	ArchiveEntries int            `json:"archive_entries"`
	MarkersFound   int            `json:"markers_found"`
	Joined         int            `json:"joined"`
	WithTranscript int            `json:"with_transcript"`
	ByStatus       map[string]int `json:"by_status"`
}

func decodeFile(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if err = d.Decode(v); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	var x any
	if err = d.Decode(&x); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode %s: trailing data", path)
	}
	return nil
}
func join(snapshotPath, evidencePath, out string) error {
	if err := privatePath(out); err != nil {
		return err
	}
	var s snapshot
	if err := decodeFile(snapshotPath, &s); err != nil {
		return err
	}
	var e evidence
	if err := decodeFile(evidencePath, &e); err != nil {
		return err
	}
	archives := map[string]archive{}
	seen := map[string]bool{}
	for _, a := range e.Archive {
		archives[a.Fingerprint] = a
	}
	findings := map[string]string{}
	for _, f := range e.Findings {
		findings[f.TaskID] = f.Status
	}
	cf := casesFile{BuiltAt: time.Now().UTC().Format(time.RFC3339), Cases: []caseOut{}, Summary: caseSummary{ArchiveEntries: len(e.Archive), ByStatus: map[string]int{}}}
	for _, t := range s.Tasks {
		ms := allMarkers(t.Content)
		cf.Summary.MarkersFound += len(ms)
		for _, m := range ms {
			a, ok := archives[m.Fingerprint]
			if !ok {
				continue
			}
			var d *delivery
			for i := range a.Deliveries {
				if a.Deliveries[i].ItemIndex == m.Index && (a.Deliveries[i].TaskID == t.ID || a.Deliveries[i].TaskID == "") {
					d = &a.Deliveries[i]
					break
				}
			}
			if d == nil {
				continue
			}
			cf.Summary.Joined++
			seen[m.Fingerprint+":"+strconv.Itoa(m.Index)] = true
			c := realCandidate{}
			if a.Extraction != nil && m.Index < len(a.Extraction.Items) {
				c = a.Extraction.Items[m.Index]
			} else {
				c = realCandidate{Kind: d.Kind, Title: d.Title, Content: d.Content}
				if c.Content == "" {
					c.Content = d.Notes
				}
			}
			status := findings[d.TaskID]
			if status != "unchanged" && status != "observed_move" && status != "missing" {
				status = "unknown"
			}
			label := "unknown"
			route := false
			if status == "unchanged" || status == "observed_move" {
				label = "supported"
				route = status == "unchanged"
			} else if status == "missing" {
				label = "unsupported"
			}
			idb := sha256.Sum256([]byte(m.Fingerprint + ":" + strconv.Itoa(m.Index)))
			co := caseOut{CaseID: hex.EncodeToString(idb[:])[:12], Split: "real", Transcript: a.Transcript, Candidate: c, ObservedStatus: status, WeakLabel: label, WeakRouteOK: route}
			cf.Cases = append(cf.Cases, co)
			cf.Summary.ByStatus[status]++
			if co.Transcript != "" {
				cf.Summary.WithTranscript++
			}
		}
	}
	// A missing remote task has no marker in the current snapshot. The archived
	// delivery still identifies its strict marker and is the only safe way to
	// retain the weak missing observation.
	for _, a := range e.Archive {
		for _, d := range a.Deliveries {
			key := a.Fingerprint + ":" + strconv.Itoa(d.ItemIndex)
			if seen[key] || findings[d.TaskID] != "missing" {
				continue
			}
			c := realCandidate{Kind: d.Kind, Title: d.Title, Content: d.Content}
			if c.Content == "" {
				c.Content = d.Notes
			}
			h := sha256.Sum256([]byte(key))
			co := caseOut{CaseID: hex.EncodeToString(h[:])[:12], Split: "real", Transcript: a.Transcript, Candidate: c, ObservedStatus: "missing", WeakLabel: "unsupported", WeakRouteOK: false}
			cf.Cases = append(cf.Cases, co)
			cf.Summary.Joined++
			cf.Summary.ByStatus["missing"]++
			if co.Transcript != "" {
				cf.Summary.WithTranscript++
			}
		}
	}
	fmt.Fprintln(os.Stdout, "WARNING: weak labels: an owner keeping an item is not proof that the item is correct.")
	return write0600(out, cf)
}
func main() {
	out := flag.String("out", "", "snapshot output path (required)")
	evidence := flag.String("evidence", "", "evidence export")
	realCases := flag.String("real-cases", "", "real case output")
	flag.Parse()
	if *evidence != "" {
		if *realCases == "" {
			fmt.Fprintln(os.Stderr, "-real-cases is required with -evidence")
			os.Exit(1)
		}
		if err := join(*out, *evidence, *realCases); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "-out is required")
		os.Exit(1)
	}
	if err := pull(*out, nil); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
