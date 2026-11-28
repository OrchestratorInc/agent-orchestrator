import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { PropsWithChildren } from "react";
import { describe, expect, it, vi } from "vitest";
import { noPRDetails } from "../lib/pr-display";
import { usePRDetailsLoad } from "./usePRDetailsLoad";
import { sessionScmSummaryQueryKey, type SessionPRSummary } from "./useSessionScmSummary";

const postMock = vi.fn(async () => ({ data: {}, error: undefined }));
vi.mock("../lib/host-clients", () => ({ clientForSessionHost: () => ({ POST: postMock }) }));

const URL = "https://github.com/acme/repo/pull/7";
const pr = (url = URL): SessionPRSummary => ({
	...noPRDetails(),
	url,
	number: 7,
	title: "Fix dashboard",
	state: "merged",
	provider: "github",
	repo: "acme/repo",
	author: "ada",
	sourceBranch: "fix/dashboard",
	targetBranch: "main",
	headSha: "abc123",
	additions: 1,
	deletions: 0,
	changedFiles: 1,
	ci: { autoInjectCI: false, state: "unknown", failingChecks: [], checks: [] },
	review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], prUrl: url, conflictFiles: [] },
	updatedAt: "2026-11-12T00:00:00Z",
});

describe("usePRDetailsLoad", () => {
	it("reads a PR's details once, then refreshes every session on the host that shows it", async () => {
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const seed = (sessionId: string, prs: SessionPRSummary[], hostId?: string) => client.setQueryData(sessionScmSummaryQueryKey(sessionId, hostId), { prs, linkedPrs: [] });
		seed("sess-1", [pr()]);
		seed("sess-2", [pr()]);
		seed("sess-3", [pr("https://github.com/acme/repo/pull/8")]);
		seed("sess-4", [pr()], "remote-host");
		const wrapper = ({ children }: PropsWithChildren) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;

		renderHook(() => usePRDetailsLoad(pr(), "sess-1"), { wrapper });

		await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
		const invalidated = (sessionId: string, hostId?: string) => client.getQueryState(sessionScmSummaryQueryKey(sessionId, hostId))?.isInvalidated;
		await waitFor(() => expect(invalidated("sess-1")).toBe(true));
		expect(invalidated("sess-2")).toBe(true);
		// Another PR, or the same URL on another host, is not this read's.
		expect(invalidated("sess-3")).toBe(false);
		expect(invalidated("sess-4", "remote-host")).toBe(false);
	});
});
