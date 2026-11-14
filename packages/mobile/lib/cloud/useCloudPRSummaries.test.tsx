import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { CloudPullRequestSummary } from "./types";
import { clearCloudPRSummaries, useCloudPRSummaries, type CloudPRSummaryLookup, type CloudPRSummaryTarget } from "./useCloudPRSummaries";

const fixtures = vi.hoisted(() => ({ cloud: {} as Record<string, unknown> }));
vi.mock("./CloudProvider", () => ({ useCloud: () => fixtures.cloud }));

const PR_URL = "https://github.com/acme/web/pull/1";
const pr = (state: string): CloudPullRequestSummary => ({
	url: PR_URL, htmlUrl: PR_URL, number: 1, title: "Mobile PR test", state, provider: "github", repository: "acme/web", author: "ao-bot",
	sourceBranch: "mobile-pr-test", targetBranch: "main", headSha: "8fc7d7e3", additions: 3, deletions: 0, changedFiles: 2,
	ci: { state: "passing", failingChecks: [] }, review: { decision: "none", hasUnresolvedHumanComments: false, unresolvedBy: [] },
	mergeability: { state: "mergeable", reasons: [], pullRequestUrl: PR_URL, conflictFiles: [] }, updatedAt: "2026-11-08T21:00:00Z",
} as unknown as CloudPullRequestSummary);

let renderer: ReactTestRenderer | undefined;
let lookup: CloudPRSummaryLookup;
function Probe({ targets }: { targets: CloudPRSummaryTarget[] }) {
	lookup = useCloudPRSummaries(targets);
	return null;
}
const flush = () => act(async () => { await Promise.resolve(); await Promise.resolve(); });

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	clearCloudPRSummaries();
});
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

// Seen on the Pixel: a pull request merged from the phone kept its open card,
// "CI passing", in the Merged list, because its detail was reused for a minute.
it("reads a session's pull requests afresh once their states change, and reuses them while they hold", async () => {
	const listSessionPullRequests = vi.fn()
		.mockResolvedValueOnce({ pullRequests: [pr("open")] })
		.mockResolvedValueOnce({ pullRequests: [pr("merged")] });
	fixtures.cloud = { client: { baseUrl: "https://cloud", listSessionPullRequests }, org: { id: "org" }, status: "ready" };

	act(() => { renderer = create(<Probe targets={[{ sessionId: "s1", stamp: "1:open" }]} />); });
	await flush();
	expect(lookup.summaryFor("s1", 1)?.state).toBe("open");

	act(() => renderer!.update(<Probe targets={[{ sessionId: "s1", stamp: "1:open" }]} />));
	await flush();
	expect(listSessionPullRequests).toHaveBeenCalledTimes(1);

	act(() => renderer!.update(<Probe targets={[{ sessionId: "s1", stamp: "1:merged" }]} />));
	await flush();
	expect(listSessionPullRequests).toHaveBeenCalledTimes(2);
	expect(lookup.summaryFor("s1", 1)?.state).toBe("merged");
});
