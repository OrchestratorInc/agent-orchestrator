package coder

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

var _ sandbox.FailureDescriber = (*Client)(nil)

// Coder workspace build statuses for a build that ended without converging.
const (
	buildStatusFailed   = "failed"
	buildStatusCanceled = "canceled"
)

// buildFailureMessageLimit bounds the build error shown to users. The full
// build log tail stays in the startup error's operator detail.
const buildFailureMessageLimit = 400

const (
	buildFailedGeneric   = "The workspace failed to build in Coder."
	buildCanceledMessage = "The workspace build was canceled in Coder before the workspace started."
	tryAgainShortly      = " Try again shortly."
)

func buildStatus(build workspaceBuild) string {
	return strings.ToLower(strings.TrimSpace(build.Status))
}

// buildFailure identifies a failed or canceled workspace build. Only a build
// that was bringing the workspace up is a startup failure: a failed stop or
// delete leaves the reconciler to behave as it does for a build in progress.
// The reason is generic; DescribeFailure reads the build log for a specific
// one when the failure is reported.
func buildFailure(build workspaceBuild) *sandbox.Failure {
	switch strings.ToLower(strings.TrimSpace(build.Transition)) {
	case "", "start":
	default:
		return nil
	}
	failure := &sandbox.Failure{ID: build.ID}
	message := buildFailedGeneric
	if buildStatus(build) == buildStatusCanceled {
		failure.Canceled = true
		message = buildCanceledMessage
	}
	failure.Err = &sandbox.StartupError{Code: sandbox.StartupErrorWorkspaceBuildFailed, Message: message}
	if jobError := strings.TrimSpace(build.Job.Error); jobError != "" {
		failure.Err.Err = errors.New(jobError)
	}
	return failure
}

// DescribeFailure explains a failed workspace build from its build log: the
// job error is usually only "terraform apply: exit status 1", while the log
// names the cause (a cloud quota, a bad parameter). A customer's own
// workspace shows the cause line, redacted; AO's own shows only the cloud
// API's error code, so it never reveals AO's accounts, subscriptions or hosts.
// The cause line always goes in Err for operators.
func (c *Client) DescribeFailure(
	ctx context.Context,
	_ sandbox.ID,
	failure sandbox.Failure,
	customerOwned bool,
) *sandbox.StartupError {
	if failure.Err == nil {
		return nil
	}
	if failure.Canceled {
		return failure.Err
	}
	line := buildErrorLine(c.buildErrorLines(ctx, failure.ID))
	if line == "" && failure.Err.Err != nil {
		line = failure.Err.Err.Error()
	}
	reason := cleanBuildError(line)
	described := &sandbox.StartupError{Code: failure.Err.Code, Message: buildFailedGeneric + tryAgainShortly}
	if reason != "" {
		described.Err = errors.New(reason)
	}
	switch {
	case customerOwned && reason != "":
		described.Message = "The workspace failed to build: " + reason
	case customerOwned:
		described.Message = buildFailedGeneric
	default:
		if code := cloudErrorCode(line); code != "" {
			described.Message = "The workspace failed to build: " + code + cloudErrorHint(code) + "." + tryAgainShortly
		}
	}
	return described
}

// buildErrorLines returns the error-level lines of a build's log. It is best
// effort: a log that cannot be read leaves the job error to explain the build.
func (c *Client) buildErrorLines(ctx context.Context, buildID string) []string {
	if buildID == "" {
		return nil
	}
	var lines []buildLogLine
	if err := c.do(ctx, http.MethodGet,
		"/api/v2/workspacebuilds/"+url.PathEscape(buildID)+"/logs", nil, &lines); err != nil {
		return nil
	}
	var found []string
	for _, line := range lines {
		if strings.EqualFold(strings.TrimSpace(line.Level), "error") {
			found = append(found, line.Output)
		}
	}
	return found
}

// buildErrorLine picks the line that names the cause. Terraform prints a
// diagnostic as an "Error: <summary>" line followed by where it happened, so
// the summary is preferred over the context lines after it.
func buildErrorLine(lines []string) string {
	first := ""
	for _, line := range lines {
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if summary, ok := strings.CutPrefix(text, "Error:"); ok && strings.TrimSpace(summary) != "" {
			return strings.TrimSpace(summary)
		}
		if first == "" {
			first = text
		}
	}
	return first
}

var (
	// awsErrorPattern finds an AWS API error code inside an SDK error
	// ("... RequestID: <id>, api error VcpuLimitExceeded: ...").
	awsErrorPattern = regexp.MustCompile(`\bapi error ([A-Za-z0-9_.]+): (.*)$`)
	// azureErrorPattern finds an Azure error code (Code="QuotaExceeded" or
	// "code": "SkuNotAvailable").
	azureErrorPattern = regexp.MustCompile(`(?i)"?\bcode"?\s*[:=]\s*"([A-Za-z0-9_.]+)"`)
	// requestIDPattern drops cloud request identifiers, which mean nothing to
	// a user and make every occurrence of one failure look different.
	requestIDPattern = regexp.MustCompile(`(?i),?\s*request ?id:\s*[A-Za-z0-9-]+`)
	spacePattern     = regexp.MustCompile(`\s+`)
)

// cloudErrorCode is the cloud API's own error code in a build error, if any.
func cloudErrorCode(text string) string {
	if match := awsErrorPattern.FindStringSubmatch(text); match != nil {
		return match[1]
	}
	if match := azureErrorPattern.FindStringSubmatch(text); match != nil {
		return match[1]
	}
	return ""
}

// cloudErrorHint says what a well-known cloud error code means.
func cloudErrorHint(code string) string {
	switch code {
	case "VcpuLimitExceeded", "InstanceLimitExceeded", "InsufficientInstanceCapacity",
		"InsufficientCapacity", "QuotaExceeded", "OperationNotAllowed",
		"SkuNotAvailable", "ZonalAllocationFailed", "AllocationFailed":
		return " (cloud capacity limit)"
	default:
		return ""
	}
}

// cleanBuildError reduces one build error to a short, redacted, single line.
func cleanBuildError(text string) string {
	text = strings.TrimSpace(activity.Excerpt(text, 0))
	if match := awsErrorPattern.FindStringSubmatch(text); match != nil {
		text = match[1] + ": " + match[2]
	}
	text = requestIDPattern.ReplaceAllString(text, "")
	text = strings.TrimSpace(spacePattern.ReplaceAllString(text, " "))
	return truncateWords(text, buildFailureMessageLimit)
}

// truncateWords cuts text to at most limit bytes on a word boundary.
func truncateWords(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const marker = "..."
	cut := text[:limit-len(marker)]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	if space := strings.LastIndexByte(cut, ' '); space > limit/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,;:") + marker
}
