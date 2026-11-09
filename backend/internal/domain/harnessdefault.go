package domain

// HarnessDefault 是 daemon 主机的 Harness 偏好；模型筛选与显式 CLI 入口独立保存。
// CLI 选择只影响新启动进程，不改变会话工作目录或厂商凭据目录。
type HarnessDefault struct {
	DisabledModels      []string `json:"disabledModels,omitempty" maxItems:"20000"`
	ExecutableDirectory string   `json:"executableDirectory,omitempty"`
	ExecutablePath      string   `json:"executablePath,omitempty"`
}

// IsZero reports whether the preference holds nothing worth storing.
func (d HarnessDefault) IsZero() bool {
	return len(d.DisabledModels) == 0 && d.ExecutableDirectory == "" && d.ExecutablePath == ""
}
