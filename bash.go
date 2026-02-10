package main

import "strings"

func wrapBashLoginCommand(command string) []string {
	return []string{"bash", "-lc", quoteForShell(command)}
}

func quoteForShell(command string) string {
	if command == "" {
		return "''"
	}

	return "'" + strings.ReplaceAll(command, "'", `'"'"'`) + "'"
}
