import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it } from "vitest";
import { useStillHere } from "./useStillHere";

describe("useStillHere", () => {
	const mount = (initial: string) => {
		let focused = true;
		let mark!: ReturnType<typeof useStillHere>;
		function Probe({ id }: { id: string }) {
			mark = useStillHere(id, () => focused);
			return null;
		}
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(createElement(Probe, { id: initial })); });
		return {
			mark: () => mark(),
			blur: () => { focused = false; },
			focus: () => { focused = true; },
			show: (id: string) => act(() => renderer.update(createElement(Probe, { id }))),
			unmount: () => act(() => renderer.unmount()),
		};
	};

	it("keeps a result for the screen that is still showing the same session", () => {
		const probe = mount("host|A");
		const here = probe.mark();
		probe.show("host|A");
		expect(here()).toBe(true);
		probe.unmount();
	});

	it("drops a result after navigating to another session, leaving, or unmounting", () => {
		const probe = mount("host|A");
		const beforeSwap = probe.mark();
		probe.show("host|B");
		expect(beforeSwap()).toBe(false);

		const beforeLeave = probe.mark();
		probe.blur();
		expect(beforeLeave()).toBe(false);
		probe.focus();

		const beforeUnmount = probe.mark();
		probe.unmount();
		expect(beforeUnmount()).toBe(false);
	});
});
