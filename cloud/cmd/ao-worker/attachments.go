package main

import (
	"context"
	"github.com/aoagents/agent-orchestrator/cloud/internal/attachments"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"net/http"
)

func (c *client) AttachmentReadGrant(ctx context.Context, id string) (attachments.ReadGrant, error) {
	var grant attachments.ReadGrant
	err := c.doMethod(ctx, http.MethodGet, "/worker/attachments/"+id+"/read-grant", nil, &grant)
	return grant, err
}
func (c *client) MaterializeAttachments(ctx context.Context, workspace string, manifest []attachments.Metadata) ([]string, error) {
	return worker.MaterializeAttachments(ctx, workspace, c.baseURL, c, manifest)
}
