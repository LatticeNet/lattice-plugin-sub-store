package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Importing the other two kinds.
//
// The migration shipped reading `/api/subs` alone, which meant an operator with
// four subscriptions, two combinations and fifteen files got four records and no
// warning that the rest had been left behind. Combinations and files are where
// the actual work lives — a combination encodes which subscriptions go together,
// and a file encodes an entire client configuration.
//
// Upstream names the file endpoint `wholeFiles`; `files` is a different, older
// route. Getting that wrong returns 404 rather than an empty list, which is at
// least loud.

// upstreamCollection is the subset of an upstream combination this plugin maps.
type upstreamCollection struct {
	Name             string            `json:"name"`
	DisplayName      string            `json:"displayName"`
	Remark           string            `json:"remark"`
	Tag              []string          `json:"tag"`
	Subscriptions    []string          `json:"subscriptions"`
	SubscriptionTags []string          `json:"subscriptionTags"`
	Process          []json.RawMessage `json:"process"`
	// IgnoreFailedRemoteSub is upstream's spelling of the failure mode: true
	// keeps serving when a member fails, false refuses.
	IgnoreFailedRemoteSub bool `json:"ignoreFailedRemoteSub"`
}

// upstreamFile is the subset of an upstream file this plugin maps.
type upstreamFile struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"displayName"`
	Remark      string            `json:"remark"`
	Tag         []string          `json:"tag"`
	Source      string            `json:"source"`
	SourceType  string            `json:"sourceType"`
	SourceName  string            `json:"sourceName"`
	Type        string            `json:"type"`
	Content     string            `json:"content"`
	URL         string            `json:"url"`
	Download    bool              `json:"download"`
	Process     []json.RawMessage `json:"process"`

	IgnoreFailedRemoteFile bool `json:"ignoreFailedRemoteFile"`
}

// upstreamFileType maps their two type names onto this plugin's.
//
// `mihomoProfile` is a client configuration whose proxies come from the named
// source; anything else is served as written.
func upstreamFileType(kind string) string {
	if strings.EqualFold(strings.TrimSpace(kind), "mihomoProfile") {
		return fileTypeConfig
	}
	return fileTypePlain
}

// scriptStepContent returns the program a Script Operator carries, and whether
// the step is one.
//
// Upstream files put their generator here rather than in `content`, which holds
// a placeholder. Every file in the deployment this was measured against does it
// this way — fifteen files, fifteen single Script Operators — so a migration
// that ignored the chain would import fifteen records whose content is the
// string "// The content of the file".
func scriptStepContent(raw json.RawMessage) (string, bool) {
	var step struct {
		Type string `json:"type"`
		Args struct {
			Mode    string `json:"mode"`
			Content string `json:"content"`
		} `json:"args"`
	}
	if err := json.Unmarshal(raw, &step); err != nil {
		return "", false
	}
	if step.Type != "Script Operator" {
		return "", false
	}
	// `mode: link` fetches the program from a URL. That is a different record
	// shape — a remote script — and pretending the URL is the program would
	// store the string "https://…" as JavaScript.
	if step.Args.Mode != "" && step.Args.Mode != "script" {
		return "", false
	}
	if strings.TrimSpace(step.Args.Content) == "" {
		return "", false
	}
	return step.Args.Content, true
}

// splitFileScript pulls a generator out of the chain.
//
// A Script Operator that assigns `$content` builds the document; one that does
// not is an ordinary node operation and stays in the chain. The distinction is
// the assignment, not the operator type, because the same type does both jobs
// upstream.
func splitFileScript(process []json.RawMessage) (script string, rest []json.RawMessage) {
	rest = make([]json.RawMessage, 0, len(process))
	for _, raw := range process {
		content, ok := scriptStepContent(raw)
		if ok && script == "" && strings.Contains(content, "$content") {
			script = content
			continue
		}
		rest = append(rest, raw)
	}
	return script, rest
}

func upstreamFailureMode(ignoreFailed bool) string {
	if ignoreFailed {
		return failureModeSkip
	}
	return failureModeStrict
}

// importUpstreamCollections maps combinations onto records.
//
// Members are rewritten through the same id derivation the subscriptions used,
// so a combination points at the records this run created rather than at names
// that mean nothing here.
func (rt *runtime) importUpstreamCollections(items []json.RawMessage, report *migrationReport, pending *[]subscriptionRecord) {
	for i, raw := range items {
		var col upstreamCollection
		if err := json.Unmarshal(raw, &col); err != nil {
			report.Skipped[fmt.Sprintf("collection-%d", i)] = "could not be decoded"
			continue
		}
		name := strings.TrimSpace(col.Name)
		if name == "" {
			report.Skipped[fmt.Sprintf("collection-%d", i)] = "has no name"
			continue
		}
		if err := validateProcess(col.Process); err != nil {
			report.Skipped[name] = err.Error()
			continue
		}
		members := make([]string, 0, len(col.Subscriptions))
		for _, member := range col.Subscriptions {
			if trimmed := strings.TrimSpace(member); trimmed != "" {
				members = append(members, migratedRecordID(trimmed))
			}
		}
		rec := subscriptionRecord{
			ID:          migratedKindID("col", name),
			Kind:        kindCollection,
			Name:        name,
			DisplayName: strings.TrimSpace(col.DisplayName),
			Remark:      strings.TrimSpace(col.Remark),
			Tags:        col.Tag,
			Members:     members,
			MemberTags:  col.SubscriptionTags,
			FailureMode: upstreamFailureMode(col.IgnoreFailedRemoteSub),
			Process:     col.Process,
			Origin:      &migratedOrigin{Source: "sub-store", Kind: "collection", Raw: raw},
		}
		if err := validateProcess(rec.Process); err != nil {
			report.Skipped[name] = err.Error()
			continue
		}
		*pending = append(*pending, rec)
		report.Imported = append(report.Imported, rec.ID)
	}
}

// importUpstreamFiles maps files onto records, lifting a generator out of the
// chain into the record's own content.
func (rt *runtime) importUpstreamFiles(items []json.RawMessage, report *migrationReport, pending *[]subscriptionRecord) {
	for i, raw := range items {
		var file upstreamFile
		if err := json.Unmarshal(raw, &file); err != nil {
			report.Skipped[fmt.Sprintf("file-%d", i)] = "could not be decoded"
			continue
		}
		name := strings.TrimSpace(file.Name)
		if name == "" {
			report.Skipped[fmt.Sprintf("file-%d", i)] = "has no name"
			continue
		}

		script, rest := splitFileScript(file.Process)
		rec := subscriptionRecord{
			ID:          migratedKindID("file", name),
			Kind:        kindFile,
			Name:        name,
			DisplayName: strings.TrimSpace(file.DisplayName),
			Remark:      strings.TrimSpace(file.Remark),
			Tags:        file.Tag,
			Source:      strings.TrimSpace(file.Source),
			URL:         strings.TrimSpace(file.URL),
			Download:    file.Download,
			// ignoreFailedRemoteFile is deliberately not mapped. A file has one
			// template, so there is no partial answer to fall back to the way a
			// combination falls back to its remaining members — the store clears
			// the field on a file for exactly that reason, and setting it here
			// would look like it had been honoured. The original value survives
			// in Origin.
			Origin: &migratedOrigin{Source: "sub-store", Kind: "file", Raw: raw},
		}
		if source := strings.TrimSpace(file.SourceName); source != "" {
			// sourceType says which namespace the name lives in. Guessing would
			// point a file at a record that happens to share a name.
			if strings.EqualFold(strings.TrimSpace(file.SourceType), "collection") {
				rec.NodeSource = migratedKindID("col", source)
			} else {
				rec.NodeSource = migratedRecordID(source)
			}
		}
		if script != "" {
			// The program is the document. Its own content was a placeholder.
			rec.FileType = fileTypeScript
			rec.Content = script
			rec.Process = rest
		} else {
			rec.FileType = upstreamFileType(file.Type)
			rec.Content = file.Content
			rec.Process = file.Process
		}
		if err := validateProcess(rec.Process); err != nil {
			report.Skipped[name] = err.Error()
			continue
		}
		*pending = append(*pending, rec)
		report.Imported = append(report.Imported, rec.ID)
	}
}

// fetchUpstreamList reads one endpoint and returns its records.
func (rt *runtime) fetchUpstreamList(base, endpoint string) ([]json.RawMessage, error) {
	status, body, err := rt.httpDo("GET", base+"/api/"+endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("list %s from the source: %s", endpoint, redactURLs(err.Error()))
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("the source returned status %d listing %s", status, endpoint)
	}
	var envelope struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("decode the source's %s list: %w", endpoint, err)
	}
	return envelope.Data, nil
}
