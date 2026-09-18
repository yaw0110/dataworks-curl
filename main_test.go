package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestEnsureNamespaceSchema(t *testing.T) {
	t.Run("prepends when missing", func(t *testing.T) {
		got := ensureNamespaceSchema("SELECT 1;")
		if !strings.HasPrefix(got, setNamespaceSchema) {
			t.Fatalf("expected SET prefix, got %q", got)
		}
		if !strings.Contains(got, "SELECT 1;") {
			t.Fatalf("expected original SQL preserved, got %q", got)
		}
	})

	t.Run("keeps existing prefix", func(t *testing.T) {
		sql := setNamespaceSchema + "\n\nSELECT 1;"
		if got := ensureNamespaceSchema(sql); got != sql {
			t.Fatalf("expected SQL unchanged, got %q", got)
		}
	})

	t.Run("case insensitive detect", func(t *testing.T) {
		sql := "set odps.namespace.schema=true;\nSELECT 1;"
		if got := ensureNamespaceSchema(sql); got != sql {
			t.Fatalf("expected no duplicate prefix, got %q", got)
		}
	})

	t.Run("empty sql", func(t *testing.T) {
		if got := ensureNamespaceSchema("  "); got != setNamespaceSchema {
			t.Fatalf("expected SET only, got %q", got)
		}
	})
}

func TestFindJobCode(t *testing.T) {
	raw := []byte(`{"code":200,"data":{"jobCode":"unified-eb89da02-7f8d-408e-8012-e361ceb0da35","status":"RUNNING"}}`)
	code, err := findJobCode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if code != "unified-eb89da02-7f8d-408e-8012-e361ceb0da35" {
		t.Fatalf("unexpected code %q", code)
	}
}

func TestFindJobCodeNestedArray(t *testing.T) {
	raw := []byte(`{"data":{"jobs":[{"nodeId":"1"},{"jobId":"unified-1234-abcd"}]}}`)
	code, err := findJobCode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if code != "unified-1234-abcd" {
		t.Fatalf("unexpected code %q", code)
	}
}

func TestFindJobCodeMissing(t *testing.T) {
	if _, err := findJobCode([]byte(`{"code":403010,"message":"illegal csrf token"}`)); err == nil {
		t.Fatal("expected error when no job code present")
	}
}

func TestResultState(t *testing.T) {
	cases := []struct {
		name string
		body string
		want jobState
	}{
		{"not ready 208", `{"code":208,"data":null,"message":"Read Job Result Error!"}`, stateRunning},
		{"not exist sentinel", `{"code":200,"data":{"headerList":[{"name":"Error"}],"bodyList":[["Result Not Exist"]]}}`, stateRunning},
		{"real error row", `{"code":200,"data":{"headerList":[{"name":"Error"}],"bodyList":[["ODPS-0130071"]]}}`, stateFailed},
		{"success with rows", `{"code":200,"data":{"headerList":[{"name":"_c0"}],"bodyList":[["1"]]}}`, stateSucceeded},
		{"success zero rows", `{"code":200,"data":{"headerList":[{"name":"id"}],"bodyList":[]}}`, stateSucceeded},
		{"empty header ambiguous", `{"code":200,"data":{"headerList":[],"bodyList":[[]]}}`, stateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resultState(parseResult([]byte(tc.body))); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestLogState(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    jobState
	}{
		{"running", "INFO Current task status:RUNNING\r\nINFO Start execute shell", stateRunning},
		{"success", "Run sql Succeed with mode\nSUCCEED: task cost time: [2383]ms", stateSucceeded},
		{"failure", "FAILED: ODPS-0130071\nERROR Shell run failed!\nERROR Current task status:ERROR", stateFailed},
		{"retry not failure", "WARN retry\nINFO Current task status:RUNNING", stateRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := logState(tc.content); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestRenderResultTable(t *testing.T) {
	body := []byte(`{"code":200,"data":{"headerList":[{"name":"id"},{"name":"dt"}],"bodyList":[["1","2026-09-17"],["2","2026-09-18"]],"exceedFlag":false}}`)
	out, ok := renderResultTable(parseResult(body))
	if !ok {
		t.Fatal("expected a table")
	}
	if !strings.Contains(out, "id\tdt") {
		t.Fatalf("unexpected header %q", out)
	}
	if !strings.Contains(out, "1\t2026-09-17") || !strings.Contains(out, "2\t2026-09-18") {
		t.Fatalf("unexpected rows %q", out)
	}
}

func TestRenderResultTableExceedFlag(t *testing.T) {
	body := []byte(`{"code":200,"data":{"headerList":[{"name":"id"}],"bodyList":[["1"]],"exceedFlag":true}}`)
	out, _ := renderResultTable(parseResult(body))
	if !strings.Contains(out, "truncated") {
		t.Fatalf("expected truncation notice, got %q", out)
	}
}

func TestParseLogContent(t *testing.T) {
	raw := []byte(`{"code":200,"data":{"jobCode":"unified-x","index":0,"content":"hello\nworld"}}`)
	if got := parseLogContent(raw); got != "hello\nworld" {
		t.Fatalf("unexpected content %q", got)
	}
	if got := parseLogContent([]byte(`{"code":1,"data":null}`)); got != "" {
		t.Fatalf("expected empty content, got %q", got)
	}
}

func TestNormalizeFlagArgs(t *testing.T) {
	got, err := normalizeFlagArgs(
		[]string{"-f", "q.sql", "--format=json", "extra"},
		map[string]bool{"f": true, "format": true},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-f", "q.sql", "--format=json", "extra"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNormalizeFlagArgsDoesNotConsumeBoolFlagAsValue(t *testing.T) {
	got, err := normalizeFlagArgs(
		[]string{"-q", "SELECT 1", "--dry-run"},
		map[string]bool{"q": true},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-q", "SELECT 1", "--dry-run"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestNormalizeFlagArgsMissingValue(t *testing.T) {
	if _, err := normalizeFlagArgs([]string{"-f"}, map[string]bool{"f": true}); err == nil {
		t.Fatal("expected error for missing flag value")
	}
}

func TestAPIErrorMessage(t *testing.T) {
	if msg := apiErrorMessage([]byte(`{"reason":"USER_NOT_LOGGED_IN","code":403001}`)); msg == "" {
		t.Fatal("expected reason to surface as error")
	}
	if msg := apiErrorMessage([]byte(`{"code":403010,"message":"illegal csrf token"}`)); msg == "" {
		t.Fatal("expected message to surface as error")
	}
	if msg := apiErrorMessage([]byte(`{"code":200,"data":{"status":"SUCCESS"}}`)); msg != "" {
		t.Fatalf("expected no error for success payload, got %q", msg)
	}
}

func TestCSRFFromCookie(t *testing.T) {
	cookie := "currentRegionId=cn-hangzhou; csrf_token=f3ddc4d0042c4b001789715754v313aefa35; isg=abc123"
	if got := csrfFromCookie(cookie); got != "f3ddc4d0042c4b001789715754v313aefa35" {
		t.Fatalf("unexpected csrf %q", got)
	}
	if got := csrfFromCookie("currentRegionId=cn-hangzhou"); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestNormalizeConfigDerivesCSRF(t *testing.T) {
	cfg := normalizeConfig(Config{Cookie: "a=1; csrf_token=xyz789; b=2"})
	if cfg.CSRF != "xyz789" {
		t.Fatalf("expected csrf derived from cookie, got %q", cfg.CSRF)
	}
	if cfg.ProjectID != defaultProjectID || cfg.DataSourceID != defaultDataSourceID {
		t.Fatalf("expected defaults filled, got %+v", cfg)
	}
}

type captureDoer struct {
	last *http.Request
	body []byte
}

func (d *captureDoer) Do(req *http.Request) (*http.Response, error) {
	d.last = req
	if d.body == nil {
		d.body = []byte(`{"code":200,"data":{"status":"SUCCESS"}}`)
	}
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(bytes.NewReader(d.body)),
		Header:     make(http.Header),
	}, nil
}

func TestGetJobLogBuildsExpectedURL(t *testing.T) {
	doer := &captureDoer{}
	client := NewClient(doer)
	cfg := normalizeConfig(Config{Cookie: "csrf_token=t; a=1", ProjectID: 672230})

	if _, err := client.GetJobLog(cfg, "unified-c66c4d11-91e9-4c23-98b6-791320ab43f4", 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if doer.last.URL.Path != "/ide/getExecutorJobLog" {
		t.Fatalf("unexpected path %q", doer.last.URL.Path)
	}
	q := doer.last.URL.Query()
	want := map[string]string{
		"extend": "true", "index": "0", "offset": "0",
		"projectId": "672230", "code": "unified-c66c4d11-91e9-4c23-98b6-791320ab43f4",
		"showScript": "false",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Fatalf("query %s = %q want %q", k, q.Get(k), v)
		}
	}
	if got := doer.last.Header.Get("x-requested-with"); got != "XMLHttpRequest" {
		t.Fatalf("missing x-requested-with, got %q", got)
	}
}

func TestBuildCreateBodyWithParams(t *testing.T) {
	cfg := normalizeConfig(Config{})
	script := ensureNamespaceSchema("select * from t where dt = '${bizdate}'")
	body, err := BuildCreateBody(cfg, script, map[string]string{"bizdate": "2026-09-17"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	paramMap, ok := payload["paramMap"].(map[string]any)
	if !ok || paramMap["bizdate"] != "2026-09-17" {
		t.Fatalf("unexpected paramMap %v", payload["paramMap"])
	}
	script2, _ := payload["scriptContent"].(string)
	if !strings.HasPrefix(script2, setNamespaceSchema) {
		t.Fatalf("expected SET prefix, got %q", script2)
	}
	if !strings.Contains(script2, "${bizdate}") {
		t.Fatalf("expected ${bizdate} preserved, got %q", script2)
	}
	if payload["dataSourceId"].(float64) != float64(defaultDataSourceID) {
		t.Fatalf("unexpected dataSourceId %v", payload["dataSourceId"])
	}
}

func TestParamFlags(t *testing.T) {
	p := &paramFlags{}
	if err := p.Set("dt=2026-09-18"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("biz=all"); err != nil {
		t.Fatal(err)
	}
	if err := p.Set("bad"); err == nil {
		t.Fatal("expected error for malformed param")
	}
	if got := p.mapValue()["dt"]; got != "2026-09-18" {
		t.Fatalf("unexpected dt %q", got)
	}
}
