package repomap

import (
	"fmt"
	"strings"

	"github.com/deqiying/fast-context/internal/dirscore"
)

type OptimizerOptions struct {
	Mode               string // classic | bootstrap_hotspot
	BootstrapTreeDepth int
	HotspotTopK        int
	HotspotTreeDepth   int
	MaxBytes           int
	RGPath             string // for probe grep; empty disables the probe signal
}

type BootstrapHints struct {
	RGPatterns []string
	HotDirs    []string
}

type OptimizedMap struct {
	Map
	HotspotDepth int
	Strategy     string
	HotDirs      []string
}

// BuildOptimized ports upstream buildOptimizedRepoMap: a shallow global tree
// plus deeper subtrees for top-K scored hotspot directories and a path-spine
// section, squeezed under MaxBytes.
func BuildOptimized(query, projectRoot string, treeDepth int, excludePaths []string, opt OptimizerOptions, hints *BootstrapHints, progress func(string)) OptimizedMap {
	log := func(msg string) {
		if progress != nil {
			progress(msg)
		}
	}
	if opt.Mode == "classic" {
		base := Build(projectRoot, treeDepth, excludePaths)
		return OptimizedMap{Map: base, Strategy: "classic"}
	}

	bootstrapDepth := clamp(opt.BootstrapTreeDepth, 1, 3)
	hotspotTopK := clamp(opt.HotspotTopK, 0, 8)
	hotspotTreeDepth := clamp(opt.HotspotTreeDepth, 1, 4)
	// The user's treeDepth raises the hotspot subtree depth so the flag is not
	// silently ignored in bootstrap_hotspot mode.
	if treeDepth > hotspotTreeDepth {
		hotspotTreeDepth = clamp(treeDepth, 1, 4)
	}
	maxBytes := opt.MaxBytes
	if maxBytes < 16*1024 {
		maxBytes = 120 * 1024
	}

	bootstrap := Build(projectRoot, bootstrapDepth, excludePaths)
	topDirs := ListTopLevelDirs(projectRoot, excludePaths)

	var keywords []string
	if hints != nil {
		keywords = hints.RGPatterns
	}

	var hotDirs []string
	var pathSpines []string
	if len(topDirs) > 0 && hotspotTopK > 0 {
		result := dirscore.ScoreDirectories(query, projectRoot, topDirs, excludePaths, dirscore.Options{
			TopK:      hotspotTopK,
			UseProbe:  true,
			Keywords:  keywords,
			MinReturn: 2,
			RGPath:    opt.RGPath,
		})
		hotDirs = result.HotDirs
		pathSpines = result.PathSpines
		log(fmt.Sprintf("BM25F scoring: hotDirs=[%s] pathSpines=%d", strings.Join(hotDirs, ","), len(pathSpines)))
		if len(hotDirs) == 0 {
			hotDirs = dirscore.QuickScore(query, topDirs, hotspotTopK)
			if len(hotDirs) == 0 && len(topDirs) > 0 {
				hotDirs = topDirs
				if len(hotDirs) > hotspotTopK {
					hotDirs = hotDirs[:hotspotTopK]
				}
			}
			log(fmt.Sprintf("Quick scoring fallback: %s", strings.Join(hotDirs, ",")))
		}
	}

	hotspotSections := make([]string, 0, len(hotDirs))
	for _, d := range hotDirs {
		hotspotSections = append(hotspotSections, BuildSubtree(projectRoot, d, hotspotTreeDepth))
	}

	pathSpineSection := ""
	if len(pathSpines) > 0 {
		var b strings.Builder
		b.WriteString("# Relevant File Paths (from BM25F path spine extraction)")
		for _, p := range pathSpines {
			b.WriteString("\n- /codebase/")
			b.WriteString(p)
		}
		pathSpineSection = b.String()
	}

	compose := func(hotspots []string, withSpines bool) string {
		var sections []string
		if len(hotspots) > 0 {
			sections = append(sections, "# Hotspot Subtrees\n"+strings.Join(hotspots, "\n\n"))
		}
		if withSpines && pathSpineSection != "" {
			sections = append(sections, pathSpineSection)
		}
		if len(sections) == 0 {
			return bootstrap.Tree
		}
		return bootstrap.Tree + "\n\n" + strings.Join(sections, "\n\n")
	}

	tree := compose(hotspotSections, true)
	if len(tree) > maxBytes {
		// First drop path spines, then progressively drop hotspot sections.
		tree = compose(hotspotSections, false)
		kept := append([]string(nil), hotspotSections...)
		for len(tree) > maxBytes && len(kept) > 0 {
			kept = kept[:len(kept)-1]
			tree = compose(kept, false)
		}
	}

	return OptimizedMap{
		Map: Map{
			Tree:      tree,
			Depth:     bootstrap.Depth,
			SizeBytes: len(tree),
			FellBack:  bootstrap.FellBack,
			AutoDepth: bootstrap.AutoDepth,
		},
		HotspotDepth: hotspotTreeDepth,
		Strategy:     "bootstrap_hotspot",
		HotDirs:      hotDirs,
	}
}

func clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
