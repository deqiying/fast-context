package search

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/deqiying/fast-context/internal/executor"
)

// 部分结果抢救借鉴 @sammysnake/fast-context-mcp 1.3.2 的 salvageSearchEvidence：
// 远端模型没有通过 answer 工具正常收尾时，从它已经产生的证据中恢复候选文件。
//
// 与上游不同，这里不扫描 system prompt 与 repo map（其中的示例路径和 path spine
// 并非模型的判断），只信任模型自身的回复文本与已经执行过的 readfile 命令；
// 每个路径都必须经执行器映射为项目内真实存在、未被排除的普通文件。
const maxSalvagedFiles = 30

// loosePathRe 匹配文本中带扩展名的 /codebase 虚拟路径。字符集刻意不含空白与逗号，
// 避免把相邻的多个路径或后续自然语言粘连成一个不存在的路径。
var loosePathRe = regexp.MustCompile(`/codebase(?:/[A-Za-z0-9_@+.()\[\]\-]+)+\.[A-Za-z0-9]{1,16}`)

type salvageCollector struct {
	exec  *executor.Executor
	root  string
	files []ResultFile
	index map[string]int
}

func newSalvageCollector(exec *executor.Executor, projectRoot string) *salvageCollector {
	return &salvageCollector{exec: exec, root: projectRoot, index: map[string]int{}}
}

// addText 先解析文本形式的 <ANSWER>（模型直接输出 XML 而未调用 answer 工具），
// 再收集散落在回复中的 /codebase 路径。
func (s *salvageCollector) addText(text string) {
	if strings.Contains(text, "<ANSWER>") {
		for _, file := range ParseAnswer(text, s.root).Files {
			s.add("/codebase/"+file.Path, file.Ranges)
		}
	}
	for _, path := range loosePathRe.FindAllString(text, -1) {
		s.add(path, nil)
	}
}

// addReadfiles 把模型主动读取过的文件（及其行范围）作为候选证据。
func (s *salvageCollector) addReadfiles(commands []executor.NamedCommand) {
	for _, named := range commands {
		command := named.Command
		if command.Type != "readfile" {
			continue
		}
		var ranges []LineRange
		if command.StartLine > 0 && command.EndLine >= command.StartLine {
			ranges = []LineRange{{Start: command.StartLine, End: command.EndLine}}
		}
		s.add(command.File, ranges)
	}
}

func (s *salvageCollector) add(virtual string, ranges []LineRange) {
	full, ok := s.exec.ResolveExistingFile(virtual)
	if !ok {
		return
	}
	if i, exists := s.index[full]; exists {
		s.files[i].Ranges = mergeRanges(s.files[i].Ranges, ranges)
		return
	}
	if len(s.files) >= maxSalvagedFiles {
		return
	}
	rel, err := filepath.Rel(s.root, full)
	if err != nil {
		return
	}
	s.index[full] = len(s.files)
	s.files = append(s.files, ResultFile{
		Path:     filepath.ToSlash(rel),
		FullPath: full,
		Ranges:   mergeRanges(make([]LineRange, 0, len(ranges)), ranges),
	})
}

func mergeRanges(existing, extra []LineRange) []LineRange {
	for _, candidate := range extra {
		duplicate := false
		for _, current := range existing {
			if current == candidate {
				duplicate = true
				break
			}
		}
		if !duplicate {
			existing = append(existing, candidate)
		}
	}
	return existing
}
