import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useRefreshActions } from "./useRefreshActions";

let actions: ReturnType<typeof useRefreshActions>;
let renderer: ReactTestRenderer | undefined;

function Probe({ refresh }: { refresh: () => Promise<unknown> }) {
	actions = useRefreshActions(refresh);
	return null;
}

/** A refresh per call, each answered by hand. */
function deferredRefreshes() {
	const answers: (() => void)[] = [];
	const refresh = vi.fn(() => new Promise<void>((resolve) => { answers.push(resolve); }));
	return { refresh, answers };
}

beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

// The stale banner's Retry stays tappable while a retry runs, so two can overlap.
it("keeps retrying until every overlapping retry has answered", async () => {
	const { refresh, answers } = deferredRefreshes();
	act(() => { renderer = create(<Probe refresh={refresh} />); });
	await act(async () => { void actions.retry(); void actions.retry(); });
	expect(refresh).toHaveBeenCalledTimes(2);
	expect(actions.retrying).toBe(true);
	await act(async () => answers[0]());
	expect(actions.retrying).toBe(true);
	await act(async () => answers[1]());
	expect(actions.retrying).toBe(false);
	expect(actions.refreshing).toBe(false);
});

it("clears retrying when a retry fails", async () => {
	const refresh = vi.fn(async () => { throw new Error("unreachable"); });
	act(() => { renderer = create(<Probe refresh={refresh} />); });
	await act(async () => { await actions.retry().catch(() => {}); });
	expect(actions.retrying).toBe(false);
});
