import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import { usePullRequestListRequest } from "./usePullRequestListRequest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
let renderer: ReactTestRenderer | undefined;
let latest: ReturnType<typeof usePullRequestListRequest>;
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; });

function Harness({ showing, show }: { showing: boolean; show(): void }) {
	latest = usePullRequestListRequest(showing, show);
	return null;
}

describe("usePullRequestListRequest", () => {
	it("bumps the request at once while the composer is showing", () => {
		const show = vi.fn();
		act(() => { renderer = create(<Harness showing show={show} />); });
		expect(latest.request).toBe(0);
		act(() => latest.ask());
		expect(latest.request).toBe(1);
		expect(show).not.toHaveBeenCalled();
	});

	// The terminal view has no composer: show the chat first, then ask, so the
	// composer mounts before the request it has to answer.
	it("shows the composer first from a view without one, then asks", () => {
		const show = vi.fn();
		act(() => { renderer = create(<Harness showing={false} show={show} />); });
		act(() => latest.ask());
		expect(show).toHaveBeenCalledOnce();
		expect(latest.request).toBe(0);
		act(() => renderer!.update(<Harness showing show={show} />));
		expect(latest.request).toBe(1);
		// Switching away and back later asks nothing more.
		act(() => renderer!.update(<Harness showing={false} show={show} />));
		act(() => renderer!.update(<Harness showing show={show} />));
		expect(latest.request).toBe(1);
	});
});
