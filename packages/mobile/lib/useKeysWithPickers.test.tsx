import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ keysUp: false }));
vi.mock("react-native", () => ({ Keyboard: { isVisible: () => fixtures.keysUp } }));

import { createEffortSliderSwitch } from "./useEffortSlider";
import { useKeysWithPickers } from "./useKeysWithPickers";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { fixtures.keysUp = false; vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

describe("keys with pickers", () => {
	// Every picker opens over the keys: opened with them down, it brings them up.
	it("focuses the field as a picker opens with the keyboard down, and only then", () => {
		const focus = vi.fn();
		const field = { current: { focus } } as never;
		const effort = createEffortSliderSwitch();
		const model = createEffortSliderSwitch();
		function Harness() { useKeysWithPickers(field, [effort, model]); return null; }
		act(() => { renderer = create(<Harness />); });
		act(() => effort.setOpen(true));
		expect(focus).toHaveBeenCalledOnce();
		act(() => effort.setOpen(false));
		fixtures.keysUp = true;
		act(() => model.setOpen(true));
		expect(focus).toHaveBeenCalledOnce();
	});
});
