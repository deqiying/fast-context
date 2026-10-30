package search

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// rejectBroadProjectRoot 借鉴 @philau2512/fast-context-mcp 1.5.7 的 project_path 保护：
// search 会把仓库地图与受限工具结果发送给 Windsurf，误把用户主目录或文件系统根
// 当作项目根会外发整个目录结构，因此必须在读取凭证与任何网络请求之前拒绝。
// 只拒绝与主目录/根目录完全相同的路径，其下的具体仓库不受影响。
func rejectBroadProjectRoot(projectRoot string) error {
	return rejectBroadProjectRootWithHome(projectRoot, userHomeDirs())
}

func rejectBroadProjectRootWithHome(projectRoot string, homes []string) error {
	for _, candidate := range pathVariants(projectRoot) {
		if filepath.Dir(candidate) == candidate {
			return fmt.Errorf("project path must not be a filesystem root: %s; pass a specific repository with --project", projectRoot)
		}
		for _, home := range homes {
			if samePath(candidate, home) {
				return fmt.Errorf("project path must not be the user home directory: %s; pass a specific repository with --project", projectRoot)
			}
		}
	}
	return nil
}

// userHomeDirs 返回主目录及其 symlink 解析结果；无法确定主目录时不做该项检查，
// 根目录检查仍然生效。
func userHomeDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return nil
	}
	return pathVariants(home)
}

func pathVariants(path string) []string {
	cleaned := filepath.Clean(path)
	variants := []string{cleaned}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		if resolved = filepath.Clean(resolved); resolved != cleaned {
			variants = append(variants, resolved)
		}
	}
	return variants
}

// samePath 在默认大小写不敏感的 Windows/macOS 文件系统上忽略大小写比较。
func samePath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
