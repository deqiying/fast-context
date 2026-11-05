package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/deqiying/fast-context/internal/doctor"
)

const doctorToolName = "fast_context_doctor"

const doctorToolDescription = "Check local readiness for fast_context_search without any network access: " +
	"whether project_path exists, which ripgrep binary is used, and which credential source is selected (key redacted). " +
	"Returns the same JSON as `fast-context doctor --format json`. " +
	"ok=false means fast_context_search is expected to fail; fix the component whose ok is false. " +
	"ok=true does not prove that the key is valid, the network is reachable, or the remote service will accept the request."

type doctorInput struct {
	ProjectPath string `json:"project_path"`
}

func registerDoctorTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        doctorToolName,
		Description: doctorToolDescription,
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"project_path"},
			"properties":           map[string]any{"project_path": projectPathProperty()},
		},
		Annotations: &mcp.ToolAnnotations{
			Title:         "Fast Context Doctor",
			ReadOnlyHint:  true,
			OpenWorldHint: boolPointer(false),
		},
	}, handleDoctor)
}

// handleDoctor 只校验非空与绝对路径；目录是否存在属于检查结论，写入报告而不作为错误返回。
// ok=false 也返回 isError=false，与 CLI doctor 退出码 0、由调用方读取 ok 的语义一致。
func handleDoctor(_ context.Context, _ *mcp.CallToolRequest, in doctorInput) (*mcp.CallToolResult, any, error) {
	projectPath, message := validateAbsolutePath(in.ProjectPath)
	if message != "" {
		return errorResult(message), nil, nil
	}
	var buffer bytes.Buffer
	// 与 CLI 非 pretty 模式相同的默认 Encoder：compact、Go 默认 HTML escaping、带结尾 LF。
	if err := json.NewEncoder(&buffer).Encode(doctor.Inspect(projectPath).JSON()); err != nil {
		return nil, nil, err
	}
	return textResult(buffer.String()), nil, nil
}
