package main

import "strings"

func ensureNamespaceSchema(sql string) string {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return setNamespaceSchema
	}
	if containsNamespaceSchema(trimmed) {
		return sql
	}
	return setNamespaceSchema + "\n\n" + sql
}

func containsNamespaceSchema(sql string) bool {
	lower := strings.ToLower(sql)
	return strings.Contains(lower, "odps.namespace.schema")
}
