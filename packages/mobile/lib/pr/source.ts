import type { CloudReviewerHarness, HarnessInspectResponse, HarnessStatus, RequestOptions, UpdateSessionPreferencesInput } from "@aoagents/cloud-client";
import type { ReviewRun, SessionPRSummary, SessionReviews } from "../api";

export type PullRequestRun = ReviewRun & { reviewerTerminalId?: string };
export type PullRequestReviews = SessionReviews & { availableReviewerHarnesses?: CloudReviewerHarness[]; created?: boolean; runs: PullRequestRun[] };
export type PullRequestPreferences = {
  reviewerHarness: string;
  workerHarness: string;
  autoReviewEnabled: boolean;
  autoInjectReview: boolean;
  autoInjectCI: boolean;
};

/** A source owns all PR requests; Cloud never receives a daemon configuration. */
export interface PullRequestSource {
  readonly kind: "local" | "cloud";
  readonly scopeKey: string;
  isCurrent(): boolean;
  list(sessionId: string, options?: RequestOptions): Promise<SessionPRSummary[]>;
  reviews(sessionId: string, options?: RequestOptions): Promise<PullRequestReviews>;
  trigger(sessionId: string, options?: RequestOptions): Promise<PullRequestReviews>;
  cancel(sessionId: string, options?: RequestOptions): Promise<PullRequestReviews>;
  merge(sessionId: string, pr: SessionPRSummary, options?: RequestOptions): Promise<void>;
  sendRun(sessionId: string, run: ReviewRun, key: string, options?: RequestOptions): Promise<void>;
  sendFeedback(sessionId: string, text: string, key: string, options?: RequestOptions): Promise<void>;
  preferences?(sessionId: string, options?: RequestOptions): Promise<PullRequestPreferences>;
  updatePreferences?(sessionId: string, input: UpdateSessionPreferencesInput, options?: RequestOptions): Promise<void>;
  inspectHarnesses?(sessionId: string, options?: RequestOptions): Promise<HarnessInspectResponse>;
  installHarness?(sessionId: string, harness: CloudReviewerHarness, options?: RequestOptions): Promise<HarnessStatus>;
}
