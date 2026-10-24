import { cancelSessionReview, getSessionPR, getSessionReviews, mergeSessionPR, sendMessage, triggerSessionReview } from "../api";
import { machineIdentity, type ServerConfig } from "../config";
import { formatReviewSummaryMessage } from "../reviewFeedback";
import type { PullRequestSource } from "./source";

export function createLocalPullRequestSource(config: ServerConfig): PullRequestSource {
  return {
    kind: "local", scopeKey: JSON.stringify(["local", machineIdentity(config)]), isCurrent: () => true,
    list: (id) => getSessionPR(config, id), reviews: (id) => getSessionReviews(config, id),
    trigger: (id) => triggerSessionReview(config, id),
    cancel: async (id) => { await cancelSessionReview(config, id); return getSessionReviews(config, id); },
    merge: (_id, pr) => mergeSessionPR(config, pr),
    sendRun: (id, run) => sendMessage(config, id, formatReviewSummaryMessage(run)),
    sendFeedback: (id, text) => sendMessage(config, id, text),
  };
}
