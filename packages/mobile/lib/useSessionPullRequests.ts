import { useRef } from "react";
import type { DashboardSession, SessionPRSummary } from "./api";
import { sessionPullRequestModel, type SessionPullRequests } from "./sessionPullRequests";

/**
 * The session's PR chip model, kept the same object while what the chip and
 * its list show is unchanged. The screen re-renders on every streamed event
 * and the composer is memoised against that; a fresh model each time would
 * re-render it, and memoising on the summaries lookup alone (a stable
 * function) would never pick up a title as it loads.
 */
export function useSessionPullRequests(
	session: Pick<DashboardSession, "pr" | "prs"> | undefined,
	summaryFor: (number: number) => SessionPRSummary | undefined,
): SessionPullRequests | undefined {
	const next = session ? sessionPullRequestModel(session, summaryFor) : undefined;
	const key = next ? JSON.stringify([
		next.rows.map((row) => [row.number, row.pr.url, row.lifecycle, row.title, row.status.text, row.status.tone]),
		next.badge.moreTone,
		next.leadSummary ?? null,
	]) : "";
	const kept = useRef<{ key: string; value: SessionPullRequests | undefined }>({ key: "", value: undefined });
	if (kept.current.key !== key) kept.current = { key, value: next };
	return kept.current.value;
}
