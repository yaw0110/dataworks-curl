package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	httpClient HTTPDoer
}

func NewClient(httpClient HTTPDoer) *Client {
	return &Client{httpClient: httpClient}
}

type createJobRequest struct {
	AppName           string            `json:"appName"`
	ParamMap          map[string]string `json:"paramMap"`
	ProjectID         int64             `json:"projectId"`
	ScriptContent     string            `json:"scriptContent"`
	NodeType          int               `json:"nodeType"`
	Language          string            `json:"language"`
	DataSourceID      int64             `json:"dataSourceId"`
	ResourceGroupCode string            `json:"resourceGroupCode"`
	ExpandMap         map[string]string `json:"expandMap"`
	EnvMap            envMap            `json:"envMap"`
}

type envMap struct {
	EngineConfig   map[string]any    `json:"engineConfig"`
	ExecutorConfig map[string]string `json:"executorConfig"`
	Version        int               `json:"version"`
}

func (c *Client) CreateJob(cfg Config, script string, params map[string]string) (string, []byte, error) {
	if cfg.Cookie == "" || cfg.CSRF == "" {
		return "", nil, errors.New("missing cookie or csrf_token; run `config --set-cookie` first")
	}
	body, err := BuildCreateBody(cfg, script, params)
	if err != nil {
		return "", nil, err
	}

	req, err := http.NewRequest("POST", cfg.BFFBase+"/ide/createExecutorJobV3", bytes.NewReader(body))
	if err != nil {
		return "", nil, err
	}
	setCommonHeaders(req, cfg)
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("Referer", ideReferer)

	raw, err := c.do(req)
	if err != nil {
		return "", raw, err
	}

	code, err := findJobCode(raw)
	if err != nil {
		return "", raw, err
	}
	return code, raw, nil
}

func BuildCreateBody(cfg Config, script string, params map[string]string) ([]byte, error) {
	if params == nil {
		params = map[string]string{}
	}
	expandMap := map[string]string{}
	if cfg.FileID != "" {
		expandMap["FILE_ID_KEY"] = cfg.FileID
	}
	if cfg.FileName != "" {
		expandMap["FILE_NAME_KEY"] = cfg.FileName
	}

	payload := createJobRequest{
		AppName:           cfg.AppName,
		ParamMap:          params,
		ProjectID:         cfg.ProjectID,
		ScriptContent:     script,
		NodeType:          defaultNodeType,
		Language:          defaultLanguage,
		DataSourceID:      cfg.DataSourceID,
		ResourceGroupCode: cfg.ResourceGroupCode,
		ExpandMap:         expandMap,
		EnvMap: envMap{
			EngineConfig:   map[string]any{},
			ExecutorConfig: map[string]string{"cu": cfg.CU},
			Version:        2,
		},
	}

	return json.Marshal(payload)
}

var jobCodePattern = regexp.MustCompile(`unified-[0-9a-zA-Z][0-9a-zA-Z-]*`)

func (c *Client) GetJobLog(cfg Config, code string, index, offset int, showScript bool) ([]byte, error) {
	if cfg.Cookie == "" {
		return nil, errors.New("missing cookie; run `config --set-cookie` first")
	}
	q := url.Values{}
	q.Set("extend", "true")
	q.Set("index", strconv.Itoa(index))
	q.Set("offset", strconv.Itoa(offset))
	q.Set("projectId", strconv.FormatInt(cfg.ProjectID, 10))
	q.Set("code", code)
	q.Set("showScript", strconv.FormatBool(showScript))

	req, err := http.NewRequest("GET", cfg.BFFBase+"/ide/getExecutorJobLog?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, cfg)
	req.Header.Set("accept", "*/*")
	req.Header.Set("origin", resultOrigin)
	req.Header.Set("referer", ideReferer)

	return c.do(req)
}

func (c *Client) GetResult(cfg Config, code string, index int) ([]byte, error) {
	if cfg.Cookie == "" {
		return nil, errors.New("missing cookie; run `config --set-cookie` first")
	}
	url := fmt.Sprintf("%s/v1/getExecutorJobResult?code=%s&index=%d&extend=true",
		cfg.BFFBase, code, index)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	setCommonHeaders(req, cfg)
	req.Header.Set("accept", "*/*")
	req.Header.Set("origin", resultOrigin)
	req.Header.Set("referer", resultReferer)

	return c.do(req)
}

func setCommonHeaders(req *http.Request, cfg Config) {
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("x-requested-with", "XMLHttpRequest")
	if cfg.CSRF != "" {
		req.Header.Set("x-csrf-token", cfg.CSRF)
	}
	req.Header.Set("Cookie", cfg.Cookie)
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// DataWorks BFF returns HTTP 200 with an error envelope, so only fail on
	// transport-level status codes and otherwise let callers inspect the body.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return body, fmt.Errorf("%s %s failed: status=%d body=%s",
			req.Method, req.URL.String(), resp.StatusCode, truncate(string(body), 500))
	}
	if msg := apiErrorMessage(body); msg != "" {
		return body, fmt.Errorf("api error: %s", msg)
	}
	return body, nil
}

func apiErrorMessage(body []byte) string {
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return ""
	}
	if _, hasData := payload["data"]; hasData {
		return ""
	}
	msg := stringValue(payload, "message", "msg", "errorMessage", "reason")
	code := payload["code"]
	if msg == "" {
		return ""
	}
	if code != nil {
		return fmt.Sprintf("%v (code=%v)", msg, code)
	}
	return msg
}

func findJobCode(raw []byte) (string, error) {
	if m := jobCodePattern.Find(raw); m != nil {
		return string(m), nil
	}
	var payload struct {
		Data *struct {
			JobCode string `json:"jobCode"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.Data != nil && payload.Data.JobCode != "" {
		return payload.Data.JobCode, nil
	}
	return "", fmt.Errorf("cannot locate job code in response: %s", truncate(string(raw), 500))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n]) + "..."
}
