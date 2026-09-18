package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

type resultPayload struct {
	Data *resultData `json:"data"`
}

type resultData struct {
	HeaderList []headerColumn `json:"headerList"`
	BodyList   [][]any        `json:"bodyList"`
	ExceedFlag bool           `json:"exceedFlag"`
}

type headerColumn struct {
	Name string `json:"name"`
}

func parseResult(raw []byte) *resultPayload {
	var p resultPayload
	if json.Unmarshal(raw, &p) != nil {
		return nil
	}
	return &p
}

type jobState int

const (
	stateUnknown jobState = iota
	stateRunning
	stateSucceeded
	stateFailed
)

// resultState interprets getExecutorJobResult. The endpoint has no status
// field: not-ready comes back as code=208 with data=null, or as the
// ["Result Not Exist"] sentinel; a finished job returns headerList.
func resultState(p *resultPayload) jobState {
	if p == nil || p.Data == nil {
		return stateRunning
	}
	if len(p.Data.HeaderList) == 0 {
		// Empty header is either a failure or a DDL/zero-column success;
		// the caller resolves it with the job log.
		return stateUnknown
	}
	if len(p.Data.HeaderList) == 1 && p.Data.HeaderList[0].Name == "Error" {
		if firstCell(p.Data.BodyList) == "Result Not Exist" {
			return stateRunning
		}
		return stateFailed
	}
	return stateSucceeded
}

func firstCell(rows [][]any) string {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ""
	}
	return formatCell(rows[0][0])
}

func resultError(p *resultPayload) string {
	if p == nil || p.Data == nil {
		return ""
	}
	if len(p.Data.HeaderList) == 1 && p.Data.HeaderList[0].Name == "Error" {
		return firstCell(p.Data.BodyList)
	}
	for _, row := range p.Data.BodyList {
		if len(row) > 0 {
			return formatCell(row[0])
		}
	}
	return ""
}

// logState reads getExecutorJobLog content, which is the only place the real
// terminal status is exposed.
func logState(content string) jobState {
	switch {
	case strings.Contains(content, "Shell run failed"),
		strings.Contains(content, "Current task status:ERROR"),
		strings.Contains(content, "Current task status:FAILED"):
		return stateFailed
	case strings.Contains(content, "SUCCEED"),
		strings.Contains(content, "Run sql Succeed"),
		strings.Contains(content, "Current task status:SUCCESS"):
		return stateSucceeded
	case strings.Contains(content, "Current task status:RUNNING"):
		return stateRunning
	}
	return stateUnknown
}

func parseLogContent(raw []byte) string {
	var payload struct {
		Data *struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Data == nil {
		return ""
	}
	return payload.Data.Content
}

func logTail(content string) string {
	lines := strings.Split(strings.TrimRight(content, "\r\n"), "\n")
	const keep = 8
	if len(lines) > keep {
		lines = lines[len(lines)-keep:]
	}
	return strings.Join(lines, "\n")
}

func renderResultTable(p *resultPayload) (string, bool) {
	if p == nil || p.Data == nil {
		return "", false
	}
	if len(p.Data.HeaderList) == 0 {
		return "(no columns; empty result)\n", true
	}
	columns := make([]string, len(p.Data.HeaderList))
	for i, col := range p.Data.HeaderList {
		columns[i] = col.Name
	}

	var b strings.Builder
	b.WriteString(strings.Join(columns, "\t"))
	b.WriteByte('\n')
	for _, row := range p.Data.BodyList {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = formatCell(cell)
		}
		b.WriteString(strings.Join(cells, "\t"))
		b.WriteByte('\n')
	}
	if p.Data.ExceedFlag {
		b.WriteString("... (truncated by server; narrow the query or add LIMIT)\n")
	}
	return b.String(), true
}

func formatCell(v any) string {
	switch typed := v.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%f", typed), "0"), ".")
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(data)
	}
}
