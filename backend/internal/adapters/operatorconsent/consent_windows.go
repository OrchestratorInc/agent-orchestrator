//go:build windows

// Package operatorconsent 将逐操作审批委托给 OS；应用不接收密码、生物信息或 PIN。
package operatorconsent

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Verifier 使用系统目录中的 Windows PowerShell 执行固定脚本，输入永不作为代码求值。
type Verifier struct{}

// New 创建原生验证器；可用性由每次 OS 验证决定，不缓存人工授权。
func New() principal.ConsentVerifier { return Verifier{} }

// Verify 先展示可滚动的完整操作详情，再请求 Windows Hello；只接受精确 Verified 结果。
func (Verifier) Verify(ctx context.Context, approval principal.Approval) error {
	directory, err := windows.GetSystemDirectory()
	if err != nil {
		return principal.ErrConsentUnavailable
	}
	windowsDirectory, err := windows.GetWindowsDirectory()
	if err != nil {
		return principal.ErrConsentUnavailable
	}
	powerShellHome := filepath.Join(directory, "WindowsPowerShell", "v1.0")
	encoded := utf16.Encode([]rune(verificationScript))
	script := make([]byte, len(encoded)*2)
	for i, code := range encoded {
		binary.LittleEndian.PutUint16(script[i*2:], code)
	}
	command := process.CommandContext(ctx, filepath.Join(powerShellHome, "powershell.exe"),
		"-NoProfile", "-NonInteractive", "-STA", "-EncodedCommand", base64.StdEncoding.EncodeToString(script))
	// 只隐藏控制台；HideWindow 会通过 STARTUPINFO 把第一个 WinForms 审批窗也隐藏。
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	// 不继承调用进程的模块、CLR profiler/DEVPATH 或 DLL 搜索环境；工作目录也只能是系统目录。
	command.Dir = directory
	command.Env = []string{
		"SystemRoot=" + windowsDirectory,
		"WINDIR=" + windowsDirectory,
		"PATH=" + directory,
		"PSModulePath=" + filepath.Join(powerShellHome, "Modules"),
		"PSModuleAnalysisCachePath=NUL",
		"PSDisableModuleAnalysisCacheCleanup=1",
	}
	input, err := json.Marshal(struct{ Method, Path, ID, Code, Digest, Payload string }{
		approval.Method, approval.Path, approval.ID, approval.Code, approval.PayloadDigest, approval.Payload,
	})
	if err != nil {
		return err
	}
	command.Stdin = strings.NewReader(string(input))
	output, err := command.Output()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return principal.ErrConsentUnavailable
	}
	if strings.TrimSpace(string(output)) != "Verified" {
		return principal.ErrConsentDenied
	}
	return nil
}

// verificationScript 不包含调用方拼接的代码；正文只通过 stdin 进入只读控件。
// 详情窗避免 Windows Hello 短提示截断正文，后续认证只由 Windows 持有凭据。
const verificationScript = `
$ErrorActionPreference = 'Stop'
$PSModuleAutoLoadingPreference = 'None'
Microsoft.PowerShell.Core\Import-Module -Name ([IO.Path]::Combine($PSHOME, 'Modules\Microsoft.PowerShell.Utility\Microsoft.PowerShell.Utility.psd1')) -ErrorAction Stop
[Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false)
$approval = Microsoft.PowerShell.Utility\ConvertFrom-Json -InputObject ([Console]::In.ReadToEnd())
if ([string]::IsNullOrWhiteSpace($approval.ID) -or [string]::IsNullOrWhiteSpace($approval.Code) -or
    [string]::IsNullOrWhiteSpace($approval.Method) -or [string]::IsNullOrWhiteSpace($approval.Path) -or
    [string]::IsNullOrWhiteSpace($approval.Digest)) { [Console]::Out.Write('InvalidRequest'); exit 0 }
$prompt = @('AO Operator', ('审批：' + $approval.ID), ('配对码：' + $approval.Code), ('摘要：' + $approval.Digest), ('操作：' + $approval.Method), ('目标：' + $approval.Path)) -join [Environment]::NewLine
$framework = [Runtime.InteropServices.RuntimeEnvironment]::GetRuntimeDirectory()
$null = [Reflection.Assembly]::LoadFrom([IO.Path]::Combine($framework, 'System.Runtime.WindowsRuntime.dll'))
$null = [Reflection.Assembly]::LoadFrom([IO.Path]::Combine($framework, 'System.Windows.Forms.dll'))
$null = [Windows.Security.Credentials.UI.UserConsentVerifier,Windows.Security.Credentials.UI,ContentType=WindowsRuntime]
$asTask = $null
foreach ($candidate in [System.WindowsRuntimeSystemExtensions].GetMethods()) {
    if ($candidate.Name -eq 'AsTask' -and $candidate.IsGenericMethod -and $candidate.GetGenericArguments().Length -eq 1 -and
        $candidate.GetParameters().Length -eq 1 -and $candidate.GetParameters()[0].ParameterType.Name -eq 'IAsyncOperation` + "`" + `1') {
        $asTask = $candidate
        break
    }
}
$availability = $asTask.MakeGenericMethod([Windows.Security.Credentials.UI.UserConsentVerifierAvailability]).Invoke($null, @([Windows.Security.Credentials.UI.UserConsentVerifier]::CheckAvailabilityAsync()))
if ($availability.GetAwaiter().GetResult().ToString() -ne 'Available') { exit 2 }
$form = [System.Windows.Forms.Form]::new()
$form.Text = 'AO Operator — 请核对完整操作内容'
$form.Width = 820
$form.Height = 640
$form.StartPosition = 'CenterScreen'
$form.TopMost = $true
$details = [System.Windows.Forms.TextBox]::new()
$details.Multiline = $true
$details.MaxLength = 0
$details.ReadOnly = $true
$details.ScrollBars = 'Both'
$details.WordWrap = $false
$details.Dock = 'Fill'
$details.Text = @($prompt, '', '精确请求正文：', $approval.Payload) -join [Environment]::NewLine
$buttons = [System.Windows.Forms.FlowLayoutPanel]::new()
$buttons.Dock = 'Bottom'
$buttons.Height = 48
$approve = [System.Windows.Forms.Button]::new()
$approve.Text = '核对后验证'
$approve.Width = 160
$approve.DialogResult = [System.Windows.Forms.DialogResult]::OK
$cancel = [System.Windows.Forms.Button]::new()
$cancel.Text = '取消'
$cancel.DialogResult = [System.Windows.Forms.DialogResult]::Cancel
$buttons.Controls.Add($approve)
$buttons.Controls.Add($cancel)
$form.Controls.Add($details)
$form.Controls.Add($buttons)
$form.CancelButton = $cancel
try {
    if ($form.ShowDialog() -ne [System.Windows.Forms.DialogResult]::OK) { [Console]::Out.Write('Canceled'); exit 0 }
} finally { $form.Dispose() }
$verification = $asTask.MakeGenericMethod([Windows.Security.Credentials.UI.UserConsentVerificationResult]).Invoke($null, @([Windows.Security.Credentials.UI.UserConsentVerifier]::RequestVerificationAsync($prompt)))
[Console]::Out.Write($verification.GetAwaiter().GetResult().ToString())
`
