package main

import (
	"fmt"
	"net/http"
	"os"
	"time"
)

func main() {
	cli, err := newCLI()
	if err != nil {
		fatal(err)
	}
	if err := cli.run(os.Args[1:]); err != nil {
		fatal(err)
	}
}

func newCLI() (*CLI, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	cfg, err := loadConfig(path)
	if err != nil {
		return nil, err
	}

	return &CLI{
		configPath: path,
		client:     NewClient(&http.Client{Timeout: 60 * time.Second}),
		config:     cfg,
		stdin:      os.Stdin,
		stdout:     os.Stdout,
		stderr:     os.Stderr,
	}, nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
