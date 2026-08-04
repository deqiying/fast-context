package commandcontract

import (
	"time"

	"github.com/deqiying/fast-context/internal/config"
)

func commandDefinitions() []CommandDefinition {
	return []CommandDefinition{
		searchDefinition(),
		keyExtractDefinition(),
		doctorDefinition(),
		skillsListDefinition(),
		skillsShowDefinition(),
		versionDefinition(),
		schemaDefinition(),
	}
}

func namespaceDefinitions() []NamespaceDefinition {
	return []NamespaceDefinition{
		{Path: []string{"key"}, Category: CategoryUtility, Summary: "Inspect explicitly selected credential sources."},
		{Path: []string{"skills"}, Category: CategoryUtility, Summary: "Inspect Agent Skills embedded in this binary."},
	}
}

func searchDefinition() CommandDefinition {
	return CommandDefinition{
		ID:           "search",
		Path:         []string{"search"},
		Category:     CategoryWorkflow,
		Summary:      "Find relevant local code through bounded semantic search.",
		Capabilities: []string{"semantic_code_search", "code_context"},
		Positionals: []PositionalDefinition{
			{
				Name:                        "query",
				Type:                        TypeString,
				Description:                 "Natural-language code search query.",
				Required:                    true,
				JoinWithSingleSpace:         true,
				MinLength:                   1,
				Pattern:                     `\S`,
				EndOfOptionsWhenLeadingDash: true,
			},
		},
		Options: []OptionDefinition{
			{
				Name:          "project",
				Flag:          "--project",
				Aliases:       []string{"-p"},
				Type:          TypeString,
				Description:   "Project root.",
				MinLength:     1,
				DefaultSource: map[string]any{"kind": "current_working_directory"},
			},
			integerOption("tree_depth", "--tree-depth", config.DefaultTreeDepth, true, config.MinTreeDepth, config.MaxTreeDepth, "Repository map depth; zero selects size-based auto depth in classic mode."),
			environmentIntegerOption("max_turns", "--max-turns", config.EnvMaxTurns, config.DefaultMaxTurns, config.MinSearchTurns, config.MaxSearchTurns, "Search rounds."),
			environmentIntegerOption("max_commands", "--max-commands", config.EnvMaxCommands, config.DefaultMaxCommands, config.MinSearchCommands, config.MaxSearchCommands, "Restricted local commands per round."),
			integerOption("max_results", "--max-results", config.DefaultMaxResults, true, config.MinSearchResults, config.MaxSearchResults, "Maximum result files."),
			{
				Name:        "timeout_ms",
				Flag:        "--timeout",
				Type:        TypeInteger,
				Description: "Stream timeout in canonical integer milliseconds.",
				Minimum:     intPointer(int(config.MinTimeout / time.Millisecond)),
				Maximum:     intPointer(int(config.MaxTimeout / time.Millisecond)),
				DefaultSource: map[string]any{
					"kind":        "environment_or_fallback",
					"environment": config.EnvTimeoutMS,
					"fallback":    int(config.DefaultTimeout / time.Millisecond),
					"environment_normalization": map[string]any{
						"kind":    "clamp",
						"minimum": int(config.MinTimeout / time.Millisecond),
						"maximum": int(config.MaxTimeout / time.Millisecond),
					},
				},
				Compatibility: map[string]any{
					"accepted_argv_syntax": []string{"go_duration", "milliseconds"},
					"explicit_flag_range":  "any_parseable_duration",
				},
				Serialize:   "base10_milliseconds",
				ArgvDecoder: "go_duration_or_integer_milliseconds",
				ParsedValue: "time.Duration",
			},
			{
				Name:        "exclude",
				Flag:        "--exclude",
				Type:        TypeStringArray,
				Description: "Additional path exclusion; repeat the flag to preserve input order.",
				Repeatable:  true,
			},
			{
				Name:        "format",
				Flag:        "--format",
				Type:        TypeString,
				Description: "Output format.",
				Default:     "text",
				HasDefault:  true,
				Enum:        []string{"text", "json"},
			},
			{
				Name:              "include_snippets",
				Flag:              "--include-snippets",
				Type:              TypeBoolean,
				Description:       "Attach bounded snippets to results.",
				EnvironmentBacked: true,
				DefaultSource: map[string]any{
					"kind":        "environment_or_fallback",
					"environment": config.EnvIncludeSnippets,
					"fallback":    false,
				},
			},
			{
				Name:        "repo_map_mode",
				Flag:        "--repo-map-mode",
				Type:        TypeString,
				Description: "Repository map strategy.",
				Enum:        []string{"classic", "bootstrap_hotspot"},
				DefaultSource: map[string]any{
					"kind":        "environment_or_fallback",
					"environment": config.EnvRepoMapMode,
					"fallback":    config.DefaultRepoMapMode,
				},
			},
			{
				Name:        "no_bootstrap",
				Flag:        "--no-bootstrap",
				Type:        TypeBoolean,
				Description: "Disable the bootstrap keyword and hotspot pre-pass for this invocation.",
				Default:     false,
				HasDefault:  true,
				Compatibility: map[string]any{
					"false_semantics": "do_not_override_runtime_bootstrap_setting",
				},
			},
			{
				Name:              "verbose",
				Flag:              "--verbose",
				Type:              TypeBoolean,
				Description:       "Print progress to stderr.",
				EnvironmentBacked: true,
				DefaultSource: map[string]any{
					"kind":        "environment_or_fallback",
					"environment": config.EnvDebug,
					"fallback":    false,
				},
			},
			prettyOption(),
		},
		Availability: AvailabilityDefinition{
			Dynamic:      true,
			CheckCommand: []string{"doctor"},
			InputBindings: []AvailabilityInputBinding{
				{Target: "project", Source: "project", OmitWhenAbsent: true},
				{Target: "format", Constant: "json"},
			},
			JSONPointer:    "/ok",
			PreflightLevel: "local_environment",
			DoesNotProve:   []string{"credential_validity", "network_reachability", "remote_service_acceptance", "search_result_quality"},
		},
		SideEffects: []string{"project_filesystem_read", "credential_source_read", "external_network", "repository_context_transmission"},
		Output: outputDefinition("text", []string{"text", "json"}, "search_result", "", []ExitCodeDefinition{
			{Code: 0, Meaning: "completed"},
			{Code: 1, Meaning: "runtime_failure"},
			{Code: 2, Meaning: "invalid_argument"},
		}),
	}
}

func keyExtractDefinition() CommandDefinition {
	return CommandDefinition{
		ID:       "key.extract",
		Path:     []string{"key", "extract"},
		Category: CategoryUtility,
		Summary:  "Inspect an explicitly selected credential source or the default Devin CLI TOML.",
		Options: []OptionDefinition{
			{
				Name:          "path",
				Flag:          "--path",
				Type:          TypeString,
				Description:   "Specific TOML or state.vscdb credential source.",
				MinLength:     1,
				DefaultSource: map[string]any{"kind": "runtime_default_credential_source", "source": "linux_wsl_devin_cli_toml"},
			},
			formatOption("text", []string{"text", "json"}),
			prettyOption(),
		},
		Availability: staticAvailability(),
		SideEffects:  []string{"credential_source_read", "temporary_filesystem_write_when_sqlite"},
		Output: outputDefinition("text", []string{"text", "json"}, "credential_extract_result", "api_key_redacted", []ExitCodeDefinition{
			{Code: 0, Meaning: "completed"},
			{Code: 1, Meaning: "credential_source_failure"},
			{Code: 2, Meaning: "invalid_argument"},
		}),
	}
}

func doctorDefinition() CommandDefinition {
	return CommandDefinition{
		ID:       "doctor",
		Path:     []string{"doctor"},
		Category: CategoryUtility,
		Summary:  "Inspect local project, ripgrep, credential, and build readiness.",
		Options: []OptionDefinition{
			{
				Name:          "project",
				Flag:          "--project",
				Aliases:       []string{"-p"},
				Type:          TypeString,
				Description:   "Project root to inspect.",
				MinLength:     1,
				DefaultSource: map[string]any{"kind": "current_working_directory"},
			},
			formatOption("text", []string{"text", "json"}),
			prettyOption(),
		},
		Availability: staticAvailability(),
		SideEffects:  []string{"environment_read", "project_filesystem_read", "credential_source_read"},
		Output: outputDefinition("text", []string{"text", "json"}, "doctor_report", "credential_material_redacted", []ExitCodeDefinition{
			{Code: 0, Meaning: "completed_check_json_ok_may_be_false"},
			{Code: 2, Meaning: "invalid_argument"},
		}),
	}
}

func skillsListDefinition() CommandDefinition {
	return CommandDefinition{
		ID:           "skills.list",
		Path:         []string{"skills", "list"},
		Aliases:      [][]string{{"skills", "ls"}},
		Category:     CategoryUtility,
		Summary:      "List Agent Skills embedded in this binary.",
		PathMatching: PathMatchingDefinition{CaseInsensitiveSegments: []int{1}},
		Options: []OptionDefinition{
			formatOptionNormalized("text", []string{"text", "json"}),
			prettyOption(),
		},
		Availability: staticAvailability(),
		SideEffects:  []string{},
		Output: outputDefinition("text", []string{"text", "json"}, "embedded_skill_list", "", []ExitCodeDefinition{
			{Code: 0, Meaning: "completed"},
			{Code: 2, Meaning: "invalid_argument"},
		}),
	}
}

func skillsShowDefinition() CommandDefinition {
	return CommandDefinition{
		ID:           "skills.show",
		Path:         []string{"skills", "show"},
		Aliases:      [][]string{{"skills", "get"}, {"skills", "read"}},
		Category:     CategoryUtility,
		Summary:      "Read one Agent Skill embedded in this binary.",
		PathMatching: PathMatchingDefinition{CaseInsensitiveSegments: []int{1}},
		Positionals: []PositionalDefinition{
			{Name: "skill", Type: TypeString, Description: "Skill ID or alias.", Required: true, MinLength: 1},
		},
		Options: []OptionDefinition{
			formatOptionNormalized("content", []string{"content", "json"}),
			prettyOption(),
		},
		Availability: staticAvailability(),
		SideEffects:  []string{"embedded_resource_read"},
		Output: outputDefinition("content", []string{"content", "json"}, "embedded_skill_content", "", []ExitCodeDefinition{
			{Code: 0, Meaning: "completed"},
			{Code: 1, Meaning: "embedded_resource_failure"},
			{Code: 2, Meaning: "invalid_argument"},
		}),
	}
}

func versionDefinition() CommandDefinition {
	return CommandDefinition{
		ID:           "version",
		Path:         []string{"version"},
		Aliases:      [][]string{{"--version"}, {"-v"}},
		Category:     CategoryUtility,
		Summary:      "Print linker-injected CLI version metadata.",
		Availability: staticAvailability(),
		SideEffects:  []string{},
		Output: outputDefinition("text", []string{"text"}, "version_line", "", []ExitCodeDefinition{
			{Code: 0, Meaning: "completed"},
			{Code: 2, Meaning: "invalid_argument"},
		}),
	}
}

func schemaDefinition() CommandDefinition {
	return CommandDefinition{
		ID:       "schema",
		Path:     []string{"schema"},
		Category: CategoryUtility,
		Summary:  "Describe public CLI commands for Agents and automation adapters.",
		Positionals: []PositionalDefinition{
			{
				Name:        "command_path",
				Type:        TypeString,
				Description: "Optional canonical command path to describe.",
				Variadic:    true,
				MinLength:   1,
				Pattern:     `^[a-z0-9][a-z0-9-]*$`,
				Lookup:      "canonical_only",
			},
		},
		Options: []OptionDefinition{
			formatOption("json", []string{"json"}),
			prettyOption(),
		},
		Availability: staticAvailability(),
		SideEffects:  []string{},
		Output: outputDefinition("json", []string{"json"}, "command_manifest", "", []ExitCodeDefinition{
			{Code: 0, Meaning: "completed"},
			{Code: 1, Meaning: "local_output_failure"},
			{Code: 2, Meaning: "invalid_argument"},
		}),
	}
}

func integerOption(name, flag string, fallback int, fixedDefault bool, minimum, maximum int, description string) OptionDefinition {
	return OptionDefinition{
		Name:        name,
		Flag:        flag,
		Type:        TypeInteger,
		Description: description,
		Default:     fallback,
		HasDefault:  fixedDefault,
		Minimum:     intPointer(minimum),
		Maximum:     intPointer(maximum),
		Clamp:       true,
	}
}

func environmentIntegerOption(name, flag, environment string, fallback, minimum, maximum int, description string) OptionDefinition {
	option := integerOption(name, flag, fallback, false, minimum, maximum, description)
	option.DefaultSource = map[string]any{
		"kind":        "environment_or_fallback",
		"environment": environment,
		"fallback":    fallback,
	}
	return option
}

func formatOption(defaultValue string, values []string) OptionDefinition {
	return OptionDefinition{
		Name:        "format",
		Flag:        "--format",
		Type:        TypeString,
		Description: "Output format.",
		Default:     defaultValue,
		HasDefault:  true,
		Enum:        values,
	}
}

func formatOptionNormalized(defaultValue string, values []string) OptionDefinition {
	option := formatOption(defaultValue, values)
	option.TrimSpaceThenLowercase = true
	return option
}

func prettyOption() OptionDefinition {
	return OptionDefinition{
		Name:        "pretty",
		Flag:        "--pretty",
		Type:        TypeBoolean,
		Description: "Indent JSON output; ignored for non-JSON formats.",
		Default:     false,
		HasDefault:  true,
	}
}

func staticAvailability() AvailabilityDefinition {
	return AvailabilityDefinition{Dynamic: false, PreflightLevel: "static"}
}

func outputDefinition(defaultFormat string, formats []string, contract, redaction string, exitCodes []ExitCodeDefinition) OutputDefinition {
	return OutputDefinition{
		DefaultFormat: defaultFormat,
		Formats:       formats,
		Contract:      contract,
		Redaction:     redaction,
		ExitCodes:     exitCodes,
	}
}

func intPointer(value int) *int {
	return &value
}
