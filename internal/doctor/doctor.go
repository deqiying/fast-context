// Package doctor 实现本地就绪检查，供 CLI `doctor` 与 MCP `fast_context_doctor` 共用。
//
// 检查只读取项目目录、ripgrep 位置与凭据来源，不访问网络、不调用 Windsurf；
// 因此报告不能证明凭据有效、网络可达或远端会接受请求。
package doctor

import (
	"os"
	"path/filepath"

	"github.com/deqiying/fast-context/internal/credentials"
	"github.com/deqiying/fast-context/internal/executor"
	"github.com/deqiying/fast-context/internal/version"
)

// Report 是一次本地就绪检查的结果。字段保持原始错误值，渲染由调用方决定。
type Report struct {
	// ProjectPath 是 filepath.Abs 的结果；Abs 失败时为空串，与既有 CLI 行为一致。
	ProjectPath   string
	ProjectExists bool

	RipgrepPath   string
	RipgrepSource string
	RipgrepErr    error

	Credentials    credentials.Info
	CredentialsErr error
	// CredentialCandidates 是可自动发现的凭据来源路径，不包含 state.vscdb。
	CredentialCandidates []string
}

// Inspect 对 project 执行检查。project 可以是相对路径，按进程当前目录解析。
func Inspect(project string) Report {
	absProject, projectErr := filepath.Abs(project)
	rgPath, rgSource, rgErr := executor.FindRipgrepWithSource()
	keyInfo, keyErr := credentials.FindAPIKey()
	return Report{
		ProjectPath:          absProject,
		ProjectExists:        projectErr == nil && dirExists(absProject),
		RipgrepPath:          rgPath,
		RipgrepSource:        rgSource,
		RipgrepErr:           rgErr,
		Credentials:          keyInfo,
		CredentialsErr:       keyErr,
		CredentialCandidates: credentials.SourceCandidates(),
	}
}

// OK 表示三项本地前提均满足；false 时 search 预期会失败。
func (r Report) OK() bool {
	return r.ProjectExists && r.RipgrepErr == nil && r.CredentialsErr == nil
}

// JSON 返回 `doctor --format json` 的报告结构。encoding/json 按键排序编码 map，
// 因此 CLI 与 MCP 使用相同 Encoder 配置时输出字节一致。API key 仅以脱敏形式出现。
func (r Report) JSON() map[string]any {
	return map[string]any{
		"ok": r.OK(),
		"project": map[string]any{
			"path":   r.ProjectPath,
			"exists": r.ProjectExists,
		},
		"ripgrep": map[string]any{
			"ok":     r.RipgrepErr == nil,
			"path":   r.RipgrepPath,
			"source": r.RipgrepSource,
			"error":  errorString(r.RipgrepErr),
		},
		"credentials": map[string]any{
			"ok":          r.CredentialsErr == nil,
			"source":      r.Credentials.SourcePath,
			"source_type": r.Credentials.SourceType,
			"key":         credentials.Redact(r.Credentials.APIKey),
			"error":       errorString(r.CredentialsErr),
			"candidates":  r.CredentialCandidates,
		},
		"version": map[string]string{
			"version": version.Version,
			"commit":  version.Commit,
			"date":    version.Date,
		},
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
