package search

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/deqiying/fast-context/internal/config"
	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/repomap"
)

// RunPipeline ports upstream searchWithContent orchestration: run the search,
// auto-retry narrower project roots when no parseable results came back,
// supplement missed files via local grep keyword expansion, and optionally
// attach code snippets. Formatting stays in the output package.
func RunPipeline(ctx context.Context, opts Options, client Client) (Result, error) {
	opts = normalizeOptions(opts)
	rgPath, _ := executor.FindRipgrep()

	runOnce := func(root string, depth int) (Result, error) {
		o := opts
		o.ProjectRoot = root
		o.TreeDepth = depth
		return Run(ctx, o, client)
	}

	hasUniquePatterns := func(r Result) bool {
		return len(unique(r.RGPatterns)) > 0
	}
	hasUsableResult := func(r Result) bool {
		return len(r.Files) > 0 || hasUniquePatterns(r)
	}

	result, err := runOnce(opts.ProjectRoot, opts.TreeDepth)
	activeProjectRoot := opts.ProjectRoot
	if abs, absErr := filepath.Abs(opts.ProjectRoot); absErr == nil {
		activeProjectRoot = abs
	}
	var retryNotes []string

	shouldRetry := err == nil && !hasUsableResult(result) && result.Raw != ""
	if shouldRetry {
		retryRoots := selectNoResultRetryProjectRoots(rgPath, activeProjectRoot, result.Meta.HotDirs, 2, opts.Query, repomap.ListTopLevelDirs)
		if len(retryRoots) > 0 {
			retryTreeDepth := opts.TreeDepth
			if retryTreeDepth == 0 || retryTreeDepth > 2 {
				retryTreeDepth = 2
			}
			retryNotes = append(retryNotes, "[retry] Initial search returned no parseable file paths; automatically retrying narrower project_path values.")
			for _, retryRoot := range retryRoots {
				retryNotes = append(retryNotes, fmt.Sprintf("[retry] tried project_path=%s, tree_depth=%d", retryRoot, retryTreeDepth))
				retryResult, retryErr := runOnce(retryRoot, retryTreeDepth)
				if retryErr == nil && hasUsableResult(retryResult) {
					result = retryResult
					err = nil
					activeProjectRoot = retryRoot
					retryNotes = append(retryNotes, fmt.Sprintf("[retry] recovered results from project_path=%s", retryRoot))
					break
				}
				if retryErr != nil {
					retryNotes = append(retryNotes, fmt.Sprintf("[retry] project_path=%s failed: %s", retryRoot, retryErr))
				} else {
					retryNotes = append(retryNotes, fmt.Sprintf("[retry] project_path=%s returned no parseable files", retryRoot))
				}
			}
		} else {
			retryNotes = append(retryNotes, "[retry] no existing likely source subdirectories found for automatic retry")
		}
	}

	result.RetryNotes = retryNotes
	result.ProjectRoot = activeProjectRoot
	if err != nil {
		return result, err
	}

	if len(result.Files) > opts.MaxResults {
		result.Files = result.Files[:opts.MaxResults]
	}
	uniquePatterns := unique(result.RGPatterns)
	result.RGPatterns = uniquePatterns

	// Grep keyword expansion: pure local, zero API calls.
	grepBudget := opts.MaxResults - len(result.Files)
	if len(uniquePatterns) > 0 && grepBudget > 0 {
		existing := make([]string, 0, len(result.Files))
		for _, f := range result.Files {
			existing = append(existing, f.Path)
		}
		extra := autoGrepFiles(rgPath, uniquePatterns, activeProjectRoot, config.MergeExcludePaths(opts.ExcludePaths), existing, 3, grepBudget)
		if len(extra) > 0 {
			result.Files = append(result.Files, extra...)
			result.GrepAdded = len(extra)
		}
	}

	if opts.IncludeSnippets {
		budgetLeft := CodeBudget
		for i := range result.Files {
			if budgetLeft <= 200 {
				break
			}
			snippets, used := readCodeSnippets(result.Files[i].FullPath, result.Files[i].Ranges, budgetLeft)
			result.Files[i].Snippets = snippets
			budgetLeft -= used
		}
	}

	return result, nil
}
