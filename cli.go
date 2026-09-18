package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type CLI struct {
	configPath string
	client     *Client
	config     Config
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
}

func (c *CLI) run(args []string) error {
	if len(args) == 0 {
		c.printUsage()
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		c.printUsage()
	case "config":
		return c.cmdConfig(args[1:])
	case "create":
		return c.cmdCreate(args[1:])
	case "result":
		return c.cmdResult(args[1:])
	case "log":
		return c.cmdLog(args[1:])
	case "query", "q":
		return c.cmdQuery(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}

func (c *CLI) printUsage() {
	fmt.Fprintf(c.stdout, `dataworks-cli - DataWorks Data Studio 自动化查数 CLI

用法:
  dataworks-cli config show
	  dataworks-cli config --set-cookie [COOKIE] [--file FILE]
  dataworks-cli config set-csrf TOKEN
  dataworks-cli config set --project-id ID [--data-source-id ID] [--resource-group CODE] [--cu 0.25]

  dataworks-cli create -f FILE.sql [-q SQL] [--param k=v ...]
  dataworks-cli result CODE [--index N] [--raw]
  dataworks-cli log CODE [--index N] [--offset N] [--show-script] [--raw]
  dataworks-cli query -f FILE.sql [-q SQL] [--param k=v ...] [--interval 2s] [--timeout 120s]
                        [--format auto|table|json|raw] [--log] [--quiet]

说明:
  所有查询会自动在开头加上必带的 %s

配置文件: %s
环境变量: DATAWORKS_CLI_CONFIG
	`, setNamespaceSchema, c.configPath)
}

func (c *CLI) cmdConfig(args []string) error {
	if len(args) == 0 || args[0] == "show" {
		cfg := c.config
		if cfg.Cookie != "" {
			cfg.Cookie = maskSecret(cfg.Cookie)
		}
		if cfg.CSRF != "" {
			cfg.CSRF = maskSecret(cfg.CSRF)
		}
		return c.printJSON(map[string]any{"config_path": c.configPath, "config": cfg})
	}

	switch args[0] {
	case "--set-cookie":
		return c.cmdSetCookie(args[1:])
	case "set-csrf":
		if len(args) < 2 {
			return errors.New("usage: config set-csrf TOKEN")
		}
		return c.update(func(cfg *Config) { cfg.CSRF = strings.TrimSpace(args[1]) })
	case "set":
		return c.cmdSet(args[1:])
	default:
		return fmt.Errorf("unknown config subcommand %q", args[0])
	}
}

func (c *CLI) cmdSetCookie(args []string) error {
	fs := flag.NewFlagSet("config --set-cookie", flag.ContinueOnError)
	file := fs.String("file", "", "read cookie string from file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var cookie string
	switch {
	case *file != "":
		data, err := os.ReadFile(*file)
		if err != nil {
			return err
		}
		cookie = strings.TrimSpace(string(data))
	case fs.NArg() >= 1 && fs.Arg(0) != "-":
		cookie = fs.Arg(0)
	default:
		line, err := bufio.NewReader(c.stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		cookie = strings.TrimSpace(line)
	}
	if cookie == "" {
		return errors.New("cookie is empty")
	}
	return c.update(func(cfg *Config) { cfg.Cookie = cookie })
}

func (c *CLI) cmdSet(args []string) error {
	fs := flag.NewFlagSet("config set", flag.ContinueOnError)
	projectID := fs.Int64("project-id", 0, "DataWorks project id")
	dataSourceID := fs.Int64("data-source-id", 0, "data source id")
	resourceGroup := fs.String("resource-group", "", "resource group code")
	cu := fs.String("cu", "", "executor CU")
	fileID := fs.String("file-id", "", "IDE file id")
	fileName := fs.String("file-name", "", "IDE file name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return c.update(func(cfg *Config) {
		if *projectID != 0 {
			cfg.ProjectID = *projectID
		}
		if *dataSourceID != 0 {
			cfg.DataSourceID = *dataSourceID
		}
		if *resourceGroup != "" {
			cfg.ResourceGroupCode = *resourceGroup
		}
		if *cu != "" {
			cfg.CU = *cu
		}
		if *fileID != "" {
			cfg.FileID = *fileID
		}
		if *fileName != "" {
			cfg.FileName = *fileName
		}
	})
}

func (c *CLI) update(mutate func(*Config)) error {
	cfg := c.config
	mutate(&cfg)
	cfg = normalizeConfig(cfg)
	if err := saveConfig(c.configPath, cfg); err != nil {
		return err
	}
	c.config = cfg
	return c.printJSON(map[string]string{"status": "ok", "config_path": c.configPath})
}

func (c *CLI) cmdCreate(args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	file := fs.String("f", "", "SQL file")
	inline := fs.String("q", "", "inline SQL")
	dryRun := fs.Bool("dry-run", false, "print request body without sending")
	params := paramFlags{}
	fs.Var(&params, "param", "paramMap entry k=v (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sql, err := resolveSQL(*file, *inline, c.stdin)
	if err != nil {
		return err
	}
	script := ensureNamespaceSchema(sql)
	if *dryRun {
		body, err := BuildCreateBody(c.config, script, params.mapValue())
		if err != nil {
			return err
		}
		fmt.Fprintln(c.stdout, string(body))
		return nil
	}
	code, raw, err := c.client.CreateJob(c.config, script, params.mapValue())
	if err != nil {
		return withRaw(err, raw, c.stderr)
	}
	fmt.Fprintln(c.stdout, "job_code:", code)
	fmt.Fprintln(c.stdout, formatJSON(raw))
	return nil
}

func (c *CLI) cmdResult(args []string) error {
	normalized, err := normalizeFlagArgs(args, map[string]bool{"index": true})
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("result", flag.ContinueOnError)
	index := fs.Int("index", 0, "result index")
	raw := fs.Bool("raw", false, "print raw response")
	if err := fs.Parse(normalized); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: result CODE [--index N] [--raw]")
	}

	body, err := c.client.GetResult(c.config, fs.Arg(0), *index)
	if err != nil {
		return withRaw(err, body, c.stderr)
	}
	if *raw {
		fmt.Fprintln(c.stdout, string(body))
		return nil
	}
	fmt.Fprintln(c.stdout, formatJSON(body))
	return nil
}

func (c *CLI) cmdLog(args []string) error {
	normalized, err := normalizeFlagArgs(args, map[string]bool{"index": true, "offset": true})
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	index := fs.Int("index", 0, "log index")
	offset := fs.Int("offset", 0, "log offset")
	showScript := fs.Bool("show-script", false, "include script in log")
	raw := fs.Bool("raw", false, "print raw response")
	if err := fs.Parse(normalized); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: log CODE [--index N] [--offset N] [--show-script] [--raw]")
	}

	body, err := c.client.GetJobLog(c.config, fs.Arg(0), *index, *offset, *showScript)
	if err != nil {
		return withRaw(err, body, c.stderr)
	}
	if *raw {
		fmt.Fprintln(c.stdout, string(body))
		return nil
	}
	fmt.Fprintln(c.stdout, formatJSON(body))
	return nil
}

func (c *CLI) cmdQuery(args []string) error {
	normalized, err := normalizeFlagArgs(args, map[string]bool{
		"f": true, "q": true, "param": true,
		"interval": true, "timeout": true, "format": true,
	})
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	file := fs.String("f", "", "SQL file")
	inline := fs.String("q", "", "inline SQL")
	interval := fs.Duration("interval", 2*time.Second, "poll interval")
	timeout := fs.Duration("timeout", 120*time.Second, "overall timeout")
	format := fs.String("format", "auto", "output format: auto|table|json|raw")
	quiet := fs.Bool("quiet", false, "only print the result payload")
	dryRun := fs.Bool("dry-run", false, "print request body without sending")
	withLog := fs.Bool("log", false, "also fetch and print the job log")
	params := paramFlags{}
	fs.Var(&params, "param", "paramMap entry k=v (repeatable)")
	if err := fs.Parse(normalized); err != nil {
		return err
	}

	sql, err := resolveSQL(*file, *inline, c.stdin)
	if err != nil {
		return err
	}
	script := ensureNamespaceSchema(sql)

	if *dryRun {
		body, err := BuildCreateBody(c.config, script, params.mapValue())
		if err != nil {
			return err
		}
		fmt.Fprintln(c.stdout, string(body))
		return nil
	}

	code, raw, err := c.client.CreateJob(c.config, script, params.mapValue())
	if err != nil {
		return withRaw(err, raw, c.stderr)
	}
	if !*quiet {
		fmt.Fprintln(c.stderr, "job_code:", code)
	}

	body, err := c.poll(code, *interval, *timeout, *quiet)
	if err != nil {
		return withRaw(err, body, c.stderr)
	}
	if err := c.printResult(body, *format); err != nil {
		return err
	}
	if *withLog {
		logBody, err := c.client.GetJobLog(c.config, code, 0, 0, false)
		if err != nil {
			return withRaw(err, logBody, c.stderr)
		}
		fmt.Fprintln(c.stdout, "--- job log ---")
		fmt.Fprintln(c.stdout, formatJSON(logBody))
	}
	return nil
}

func (c *CLI) poll(code string, interval, timeout time.Duration, quiet bool) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	var last []byte
	for {
		body, err := c.client.GetResult(c.config, code, 0)
		if err != nil {
			return body, err
		}
		last = body
		p := parseResult(body)

		switch resultState(p) {
		case stateSucceeded:
			return body, nil
		case stateFailed:
			return nil, fmt.Errorf("job %s failed: %s", code, resultError(p))
		}

		// Empty header means either a failure or a zero-column success (DDL),
		// which the result endpoint cannot distinguish. Ask the log.
		if p != nil && p.Data != nil && len(p.Data.HeaderList) == 0 {
			logBody, logErr := c.client.GetJobLog(c.config, code, 0, 0, false)
			if logErr == nil {
				content := parseLogContent(logBody)
				switch logState(content) {
				case stateFailed:
					return nil, fmt.Errorf("job %s failed:\n%s", code, logTail(content))
				case stateSucceeded:
					return body, nil
				}
			}
		}

		if !quiet {
			fmt.Fprintf(c.stderr, "waiting... (%s)\n", time.Now().Format("15:04:05"))
		}
		if time.Now().After(deadline) {
			return last, fmt.Errorf("job %s timed out after %s", code, timeout)
		}
		time.Sleep(interval)
	}
}

func (c *CLI) printResult(body []byte, format string) error {
	switch format {
	case "raw":
		fmt.Fprintln(c.stdout, string(body))
		return nil
	case "json":
		fmt.Fprintln(c.stdout, formatJSON(body))
		return nil
	}

	p := parseResult(body)
	out, ok := renderResultTable(p)
	if !ok {
		if format == "table" {
			return errors.New("no tabular data found; use --format json to inspect the payload")
		}
		fmt.Fprintln(c.stdout, formatJSON(body))
		return nil
	}
	fmt.Fprint(c.stdout, out)
	return nil
}

func (c *CLI) printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(c.stdout, string(data))
	return nil
}

func resolveSQL(file, inline string, stdin io.Reader) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	if inline != "" {
		return inline, nil
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", err
	}
	sql := strings.TrimSpace(string(data))
	if sql == "" {
		return "", errors.New("no SQL provided; use -f FILE, -q SQL, or pipe via stdin")
	}
	return sql, nil
}

func withRaw(err error, raw []byte, w io.Writer) error {
	if len(raw) > 0 {
		fmt.Fprintln(w, "raw response:", formatJSON(raw))
	}
	return err
}

type paramFlags struct {
	values map[string]string
}

func (p *paramFlags) String() string { return "" }

func (p *paramFlags) Set(v string) error {
	idx := strings.Index(v, "=")
	if idx <= 0 {
		return fmt.Errorf("invalid --param %q, want k=v", v)
	}
	if p.values == nil {
		p.values = map[string]string{}
	}
	p.values[v[:idx]] = v[idx+1:]
	return nil
}

func (p *paramFlags) mapValue() map[string]string {
	if p.values == nil {
		return map[string]string{}
	}
	return p.values
}

func maskSecret(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "***" + s[len(s)-4:]
}
