package specgen

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"net/http"
)

// qualityOperations 将正式质量路由及精确批准正文纳入同一代码生成契约。
func qualityOperations() []operation {
	errors := []respUnit{{400, envelope.APIError{}}, {403, envelope.APIError{}}, {404, envelope.APIError{}}, {409, envelope.APIError{}}, {500, envelope.APIError{}}, {501, envelope.APIError{}}}
	return []operation{
		{method: "GET", path: "/api/v1/workflows/{workflowId}/quality-summary", id: "getWorkflowQualitySummary", tag: "workflows", summary: "getWorkflowQualitySummary", pathParams: []any{controllers.WorkflowIDParam{}}, resps: append([]respUnit{{http.StatusOK, controllers.WorkflowQualitySummaryResponse{}}}, errors...)},
		{method: "GET", path: "/api/v1/workflows/{workflowId}/coordination-summary", id: "getWorkflowCoordinationSummary", tag: "workflows", summary: "getWorkflowCoordinationSummary", pathParams: []any{controllers.WorkflowIDParam{}}, resps: append([]respUnit{{http.StatusOK, controllers.WorkflowCoordinationResponse{}}}, errors...)},
		{method: "GET", path: "/api/v1/workflows/{workflowId}/quality-policies", id: "listWorkflowQualityPolicies", tag: "workflows", summary: "listWorkflowQualityPolicies", pathParams: []any{controllers.WorkflowIDParam{}}, resps: append([]respUnit{{http.StatusOK, controllers.QualityPoliciesResponse{}}}, errors...)},
		{method: "PUT", path: "/api/v1/workflows/{workflowId}/quality-policy", id: "configureWorkflowQualityPolicy", tag: "workflows", summary: "configureWorkflowQualityPolicy", pathParams: []any{controllers.WorkflowIDParam{}}, reqBody: controllers.QualityPolicyRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.QualityPolicyResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/attempts/{attemptId}/artifacts/snapshot", id: "snapshotWorkflowArtifacts", tag: "workflows", summary: "snapshotWorkflowArtifacts", pathParams: []any{controllers.WorkflowAttemptIDParam{}}, reqBody: controllers.QualitySnapshotRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.QualityArtifactsResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/artifacts/{artifactId}/gates", id: "runWorkflowArtifactGate", tag: "workflows", summary: "runWorkflowArtifactGate", pathParams: []any{controllers.QualityArtifactIDParam{}}, reqBody: controllers.QualityActionRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.QualityGateResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/artifacts/{artifactId}/reviews", id: "beginWorkflowArtifactReview", tag: "workflows", summary: "beginWorkflowArtifactReview", pathParams: []any{controllers.QualityArtifactIDParam{}}, reqBody: controllers.QualityActionRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.QualityReviewResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/reviews/{reviewId}/finish", id: "finishWorkflowArtifactReview", tag: "workflows", summary: "finishWorkflowArtifactReview", pathParams: []any{controllers.QualityReviewIDParam{}}, reqBody: controllers.QualityReviewFinishRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.QualityReviewResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/artifacts/{artifactId}/commit", id: "commitWorkflowArtifact", tag: "workflows", summary: "commitWorkflowArtifact", pathParams: []any{controllers.QualityArtifactIDParam{}}, reqBody: controllers.QualityActionRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.QualityCommitResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/integrations/preview", id: "previewWorkflowIntegration", tag: "workflows", summary: "previewWorkflowIntegration", pathParams: []any{controllers.WorkflowIDParam{}}, reqBody: controllers.QualityPreviewRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.IntegrationPlanResponse{}}}, errors...)},
		{method: "GET", path: "/api/v1/workflows/{workflowId}/integrations/{planId}", id: "getWorkflowIntegrationPlan", tag: "workflows", summary: "getWorkflowIntegrationPlan", pathParams: []any{controllers.IntegrationPlanIDParam{}}, resps: append([]respUnit{{http.StatusOK, controllers.IntegrationPlanResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/integrations/{planId}/apply", id: "applyWorkflowIntegration", tag: "workflows", summary: "applyWorkflowIntegration", pathParams: []any{controllers.IntegrationPlanIDParam{}}, reqBody: domain.IntegrationApplyRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.IntegrationOperationResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/integrations/{planId}/push", id: "pushWorkflowIntegration", tag: "workflows", summary: "pushWorkflowIntegration", pathParams: []any{controllers.IntegrationPlanIDParam{}}, reqBody: domain.IntegrationApplyRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.IntegrationOperationResponse{}}}, errors...)},
		{method: "GET", path: "/api/v1/workflows/{workflowId}/integration-operations/{operationId}", id: "getWorkflowIntegrationOperation", tag: "workflows", summary: "getWorkflowIntegrationOperation", pathParams: []any{controllers.IntegrationOperationIDParam{}}, resps: append([]respUnit{{http.StatusOK, controllers.IntegrationOperationResponse{}}}, errors...)},
		{method: "POST", path: "/api/v1/workflows/{workflowId}/integration-operations/{operationId}/reconcile", id: "reconcileWorkflowIntegrationOperation", tag: "workflows", summary: "reconcileWorkflowIntegrationOperation", pathParams: []any{controllers.IntegrationOperationIDParam{}}, reqBody: controllers.QualityReconcileRequest{}, resps: append([]respUnit{{http.StatusOK, controllers.IntegrationOperationResponse{}}}, errors...)},
	}
}
