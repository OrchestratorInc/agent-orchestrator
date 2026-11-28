import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useValueFeed } from "./useValueFeed";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

let renderer: ReactTestRenderer | undefined;
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; });

let subscribe: ((listener: (value: number) => void) => () => void) | undefined;
function Feed({ value }: { value: number }) {
	subscribe = useValueFeed(value);
	return null;
}

describe("useValueFeed", () => {
	it("gives the latest value at once and every change after, until unsubscribed", () => {
		act(() => { renderer = create(<Feed value={1} />); });
		const first = subscribe;
		const listener = vi.fn();
		const stop = subscribe!(listener);
		expect(listener).toHaveBeenLastCalledWith(1);
		act(() => renderer!.update(<Feed value={2} />));
		expect(listener).toHaveBeenLastCalledWith(2);
		// The same function across renders, so a parked sheet entry keeps working.
		expect(subscribe).toBe(first);
		stop();
		act(() => renderer!.update(<Feed value={3} />));
		expect(listener).toHaveBeenCalledTimes(2);
		// A later subscriber starts from the newest value.
		const late = vi.fn();
		subscribe!(late);
		expect(late).toHaveBeenCalledWith(3);
	});
});
