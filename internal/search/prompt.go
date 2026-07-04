package search

import (
	"encoding/json"
	"fmt"
	"strings"
)

const finalForceAnswer = "You have no turns left. Now you MUST provide your final ANSWER, even if it's not complete."

const systemPromptTemplate = `You are an expert software engineer, responsible for providing context to another engineer to solve a code issue in the current codebase.

The user will present you with a description of the issue, and it is your job to provide file paths with associated line ranges that contain ALL information relevant to understand and correctly address the issue.

# IMPORTANT
- Include entire semantic blocks such as functions, classes, definitions, and config sections.
- Do not return irrelevant files just to provide output.
- Working directory: /codebase.
- Tool access: use the restricted_exec tool ONLY.
- Allowed sub-commands: rg, readfile, tree, ls, glob.
- You must use a SINGLE restricted_exec call, with at most {max_commands} commands.
- You have at most {max_turns} turns to interact with the environment.
- Your final response must call the answer tool with XML:
  <ANSWER><file path="/codebase/path"><range>10-60</range></file></ANSWER>
- Aim to return at most {max_results} files.
`

func buildSystemPrompt(maxTurns, maxCommands, maxResults int) string {
	out := strings.ReplaceAll(systemPromptTemplate, "{max_turns}", fmt.Sprint(maxTurns))
	out = strings.ReplaceAll(out, "{max_commands}", fmt.Sprint(maxCommands))
	out = strings.ReplaceAll(out, "{max_results}", fmt.Sprint(maxResults))
	return out
}

func buildToolDefinitions(maxCommands int) string {
	props := map[string]any{}
	for i := 1; i <= maxCommands; i++ {
		props[fmt.Sprintf("command%d", i)] = commandSchema(i)
	}
	tools := []map[string]any{
		{
			"type": "function",
			"function": map[string]any{
				"name":        "restricted_exec",
				"description": "Execute restricted commands (rg, readfile, tree, ls, glob) in parallel.",
				"parameters": map[string]any{
					"type":       "object",
					"properties": props,
					"required":   []string{"command1"},
				},
			},
		},
		{
			"type": "function",
			"function": map[string]any{
				"name":        "answer",
				"description": "Final answer with relevant files and line ranges.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"answer": map[string]any{
							"type":        "string",
							"description": "The final answer in XML format.",
						},
					},
					"required": []string{"answer"},
				},
			},
		},
	}
	data, _ := json.Marshal(tools)
	return string(data)
}

func commandSchema(n int) map[string]any {
	return map[string]any{
		"type":        "object",
		"description": fmt.Sprintf("Command %d to execute. Must be one of: rg, readfile, tree, ls, glob.", n),
		"oneOf": []map[string]any{
			{
				"properties": map[string]any{
					"type":    map[string]any{"type": "string", "const": "rg"},
					"pattern": map[string]any{"type": "string"},
					"path":    map[string]any{"type": "string"},
					"include": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"exclude": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				},
				"required": []string{"type", "pattern", "path"},
			},
			{
				"properties": map[string]any{
					"type":       map[string]any{"type": "string", "const": "readfile"},
					"file":       map[string]any{"type": "string"},
					"start_line": map[string]any{"type": "integer"},
					"end_line":   map[string]any{"type": "integer"},
				},
				"required": []string{"type", "file"},
			},
			{
				"properties": map[string]any{
					"type":   map[string]any{"type": "string", "const": "tree"},
					"path":   map[string]any{"type": "string"},
					"levels": map[string]any{"type": "integer"},
				},
				"required": []string{"type", "path"},
			},
			{
				"properties": map[string]any{
					"type":        map[string]any{"type": "string", "const": "ls"},
					"path":        map[string]any{"type": "string"},
					"long_format": map[string]any{"type": "boolean"},
					"all":         map[string]any{"type": "boolean"},
				},
				"required": []string{"type", "path"},
			},
			{
				"properties": map[string]any{
					"type":        map[string]any{"type": "string", "const": "glob"},
					"pattern":     map[string]any{"type": "string"},
					"path":        map[string]any{"type": "string"},
					"type_filter": map[string]any{"type": "string", "enum": []string{"file", "directory", "all"}},
				},
				"required": []string{"type", "pattern", "path"},
			},
		},
	}
}
