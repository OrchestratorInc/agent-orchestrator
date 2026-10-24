import { describe, expect, it, vi } from "vitest";
import { type CloudClient, type PullRequestSummary, type AOReviewRun } from "@aoagents/cloud-client";
import { createCloudPullRequestSource, mapCloudPR, mapCloudReviews } from "./pullRequests";

const run = { id: "run", reviewId: "review", sessionId: "session", batchId: "batch", harness: "cursor", triggerSource: "manual", pullRequestUrl: "https://github.com/a/b/pull/7", targetSha: "sha", status: "complete", verdict: "approved", body: "Findings", providerReviewId: "123", createdAt: "now", autoInjectReview: false, reviewerTerminalId: "terminal" } satisfies AOReviewRun;
const state = { sessionId: "session", reviewerHandleId: "terminal", availableReviewerHarnesses: ["codex" as const], reviews: [{ pullRequestUrl: run.pullRequestUrl, pullRequestNumber: 7, title: "Fix", targetSha: "sha", status: "up_to_date" as const, latestRun: run }], runs: [run] };
describe("Cloud PR source", () => {
  it("preserves provider identity, inline feedback, resolved comments and reviewer terminal facts", () => {
    const pr = mapCloudPR({ repository: "a/b", number: 7, url: run.pullRequestUrl, state: "draft", review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [], resolvedBy: [{ reviewerId: "me", count: 1, links: [{ reviewId: "123", body: "Fix it", autoInjectReview: false }] }], reviews: [] }, mergeability: { state: "unknown", reasons: [], pullRequestUrl: run.pullRequestUrl } } as unknown as PullRequestSummary);
    expect(pr).toMatchObject({ repo: "a/b", state: "draft", review: { resolvedBy: [{ links: [{ body: "Fix it", reviewId: "123" }] }] }, mergeability: { prUrl: run.pullRequestUrl } });
    expect(mapCloudReviews(state)).toMatchObject({ reviewerHandleId: "terminal", availableReviewerHarnesses: ["codex"], runs: [{ id: "run", prUrl: run.pullRequestUrl, githubReviewId: "123", harness: "cursor", reviewerTerminalId: "terminal" }] });
    expect(mapCloudReviews(state).reviewerSurface).toBeUndefined();
  });
  it("uses stored-run delivery with the caller's stable idempotency key", async () => {
    const send = vi.fn(async () => ({}));
    const client = { getSession: async () => ({ session: { desiredState: "running", runtimeConnected: true } }), sendSessionReviewToWorker: send } as unknown as CloudClient;
    const source = createCloudPullRequestSource({ client, orgId: "org", scopeKey: "account", isCurrent: () => true });
    const options = { signal: new AbortController().signal };
    await source.sendRun("session", mapCloudReviews(state).runs[0], "same-key", options);
    expect(send).toHaveBeenCalledWith("org", "session", "run", { idempotencyKey: "same-key", ...options });
  });
  it("rejects stale and cancelled reads, including late completions", async () => {
    let current = true; let finish!: (value: unknown) => void;
    const list = vi.fn(() => new Promise((resolve) => { finish = resolve; }));
    const source = createCloudPullRequestSource({ client: { listSessionPullRequests: list } as unknown as CloudClient, orgId: "org", scopeKey: "account", isCurrent: () => current });
    const pending = source.list("session"); current = false; finish({ pullRequests: [] });
    await expect(pending).rejects.toThrow(/account changed/i);
    await expect(source.trigger("session")).rejects.toThrow(/account changed/i);
    current = true; const controller = new AbortController(); controller.abort();
    await expect(source.list("session", { signal: controller.signal })).rejects.toMatchObject({ name: "AbortError" });
    expect(list).toHaveBeenCalledTimes(1);
  });
});
