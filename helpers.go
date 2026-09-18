package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func formatJSON(data []byte) string {
	var v any
	if json.Unmarshal(data, &v) == nil {
		var out bytes.Buffer
		enc := json.NewEncoder(&out)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if enc.Encode(v) == nil {
			return strings.TrimRight(out.String(), "\n")
		}
	}
	return string(data)
}

func stringValue(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := m[key]; ok {
			if typed, ok := value.(string); ok {
				return typed
			}
		}
	}
	return ""
}

func csrfFromCookie(cookie string) string {
	for _, part := range strings.Split(cookie, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && name == "csrf_token" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func normalizeFlagArgs(args []string, flagsWithValue map[string]bool) ([]string, error) {
	var flagArgs []string
	var positionals []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}

		flagArgs = append(flagArgs, arg)
		name := strings.TrimLeft(arg, "-")
		if idx := strings.Index(name, "="); idx >= 0 {
			name = name[:idx]
		}
		if !flagsWithValue[name] {
			continue
		}
		if strings.Contains(arg, "=") {
			continue
		}
		if i+1 >= len(args) {
			return nil, fmt.Errorf("flag %s requires a value", arg)
		}
		i++
		flagArgs = append(flagArgs, args[i])
	}

	return append(flagArgs, positionals...), nil
}
