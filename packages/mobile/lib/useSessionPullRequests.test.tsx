import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it } from "vitest";
import type { DashboardPR, SessionPRSummary } from "./api";
import type { SessionPullRequests } from "./sessionPullRequests";
import { useSessionPullRequests } from "./useSessionPullRequests";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
const pr = (number: number, state: "open" | "merged" | "closed"): DashboardPR => ({ number, url: "", state }) as DashboardPR;
const titled = (number: number, title: string) => ({ number, title, state: "open", ci: { state: "unknown", failingChecks: [] }, mergeability: { state: "unknown" }, review: { decision: "none", hasUnresolvedHumanComments: false } }) as unknown as SessionPRSummary;

let renderer: ReactTestRenderer | undefined;
let seen: (SessionPullRequests | undefined)[] = [];
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; seen = []; });

function Harness({ prs, titles }: { prs: DashboardPR[]; titles: Record<number, string> }) {
	seen.push(useSessionPullRequests({ prs }, (n) => (titles[n] ? titled(n, titles[n]) : undefined)));
	return null;
}

describe("useSessionPullRequests", () => {
	it("keeps the same model while what the chip shows is unchanged", () => {
		const prs = [pr(9137, "open"), pr(9125, "merged")];
		act(() => { renderer = create(<Harness prs={prs} titles={{}} />); });
		act(() => renderer!.update(<Harness prs={[...prs]} titles={{}} />));
		expect(seen).toHaveLength(2);
		expect(seen[1]).toBe(seen[0]);
	});

	it("rebuilds when a title loads or a PR changes state", () => {
		const prs = [pr(9137, "open"), pr(9125, "merged")];
		act(() => { renderer = create(<Harness prs={prs} titles={{}} />); });
		act(() => renderer!.update(<Harness prs={prs} titles={{ 9137: "Create brand doc" }} />));
		expect(seen[1]).not.toBe(seen[0]);
		expect(seen[1]!.rows[0].title).toBe("Create brand doc");
		act(() => renderer!.update(<Harness prs={[pr(9137, "merged"), pr(9125, "merged")]} titles={{ 9137: "Create brand doc" }} />));
		expect(seen[2]!.lead.state).toBe("merged");
	});

	it("is undefined with no pull request", () => {
		act(() => { renderer = create(<Harness prs={[]} titles={{}} />); });
		expect(seen[0]).toBeUndefined();
	});
});
