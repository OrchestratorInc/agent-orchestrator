import { CloudApiError, type AOReviewRun, type CloudClient, type PullRequestSummary, type SessionReviewState } from "@aoagents/cloud-client";
import type { SessionPRSummary } from "../api";
import type { PullRequestRun, PullRequestSource, PullRequestReviews } from "../pr/source";
import { throwIfCloudRequestAborted } from "./cancellation";
import { cloudRequestErrorCopy } from "./errorCopy";

export function mapCloudPR(pr: PullRequestSummary): SessionPRSummary {
  const { repository, mergeability, ...facts } = pr;
  const { pullRequestUrl, ...merge } = mergeability;
  return { ...facts, repo: repository, mergeability: { ...merge, prUrl: pullRequestUrl } };
}

function mapRun(run: AOReviewRun): PullRequestRun {
  const { pullRequestUrl, providerReviewId, ...facts } = run;
  return { ...facts, prUrl: pullRequestUrl, githubReviewId: providerReviewId };
}

export function mapCloudReviews(state: SessionReviewState): PullRequestReviews {
  return {
    reviewerHandleId: state.reviewerHandleId ?? "", reviewerHarness: state.reviewerHarness,
    availableReviewerHarnesses: state.availableReviewerHarnesses ?? [],
    reviews: (state.reviews ?? []).map(({ pullRequestUrl, pullRequestNumber, latestRun, previousRun, ...facts }) => ({
      ...facts, prUrl: pullRequestUrl, prNumber: pullRequestNumber,
      latestRun: latestRun ? mapRun(latestRun) : undefined,
      previousRun: previousRun ? mapRun(previousRun) : undefined,
    })),
    runs: (state.runs ?? []).map(mapRun),
  };
}

export function createCloudPullRequestSource(input: { client: CloudClient; orgId: string; scopeKey: string; isCurrent: () => boolean }): PullRequestSource {
  const { client, orgId, scopeKey, isCurrent } = input;
  const requireCurrent = (signal?: AbortSignal) => {
    if (!isCurrent()) throw new Error("Cloud account changed. Open Pull Requests from your current workspace.");
    throwIfCloudRequestAborted(signal);
  };
  async function request<T>(run: () => Promise<T>, signal?: AbortSignal): Promise<T> {
    requireCurrent(signal);
    try { const result = await run(); requireCurrent(signal); return result; }
    catch (cause) {
      requireCurrent(signal);
      if (cause instanceof CloudApiError) {
        if (cause.code === "pr_not_mergeable" || cause.code === "PR_NOT_MERGEABLE") throw new Error("This pull request changed or is no longer ready to merge. Refresh its status and try again.");
        if (cause.code === "WORKER_UNAVAILABLE") throw new Error("Reconnect this Cloud worker before using review actions.");
        throw new Error(cloudRequestErrorCopy(cause, { includeReference: false }));
      }
      if (cause instanceof TypeError) throw new Error("Couldn't connect to Cloud. Check your connection and try again.");
      throw cause;
    }
  }
  async function requireRunning(id: string, signal?: AbortSignal) {
    const { session } = await request(() => client.getSession(orgId, id, { signal }), signal);
    if (session.desiredState !== "running" || !session.runtimeConnected || session.isTerminated) throw new Error("Return to this Cloud worker and resume or reconnect it before sending feedback.");
  }
  return {
    kind: "cloud", scopeKey, isCurrent,
    list: async (id, options) => ((await request(() => client.listSessionPullRequests(orgId, id, options), options?.signal)).pullRequests ?? []).map(mapCloudPR),
    reviews: async (id, options) => mapCloudReviews(await request(() => client.getSessionReviewState(orgId, id, options), options?.signal)),
    trigger: async (id, options) => mapCloudReviews(await request(() => client.triggerSessionReviews(orgId, id, options), options?.signal)),
    cancel: async (id, options) => mapCloudReviews(await request(() => client.cancelSessionReviews(orgId, id, options), options?.signal)),
    merge: async (id, pr, options) => {
      if (!pr.headSha) throw new Error("Refresh this pull request before merging; its head commit is missing.");
      await request(() => client.mergePullRequest(orgId, id, pr.number, { prUrl: pr.url, expectedHeadSha: pr.headSha! }, options), options?.signal);
    },
    sendRun: async (id, run, key, options) => {
      await requireRunning(id, options?.signal);
      await request(() => client.sendSessionReviewToWorker(orgId, id, run.id, { ...options, idempotencyKey: key }), options?.signal);
    },
    sendFeedback: async (id, text, key, options) => {
      await requireRunning(id, options?.signal);
      await request(() => client.sendMessage(orgId, id, text, { ...options, idempotencyKey: key }), options?.signal);
    },
    preferences: async (id, options) => {
      const { session } = await request(() => client.getSession(orgId, id, options), options?.signal);
      return { reviewerHarness: session.reviewerHarness ?? "", workerHarness: session.harness, autoReviewEnabled: session.autoReviewEnabled, autoInjectReview: session.autoInjectReview, autoInjectCI: session.autoInjectCI };
    },
    updatePreferences: async (id, value, options) => { await request(() => client.updateSessionPreferences(orgId, id, value, options), options?.signal); },
    inspectHarnesses: (id, options) => request(() => client.inspectSessionReviewerHarnesses(orgId, id, options), options?.signal),
    installHarness: (id, harness, options) => request(() => client.installSessionReviewerHarness(orgId, id, harness, options), options?.signal),
  };
}
