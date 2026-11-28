import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { describe, expect, it, vi } from "vitest";
import { useWakeOnShow } from "./useWakeOnShow";

function Probe({ asleep, shown, starting, start }: { asleep: boolean; shown: boolean; starting: boolean; start: () => void }) {
	useWakeOnShow(asleep, shown, starting, start);
	return null;
}

function mount(props: { asleep: boolean; shown: boolean; starting: boolean; start: () => void }) {
	let view!: ReactTestRenderer;
	act(() => { view = create(<Probe {...props} />); });
	return (next: Partial<typeof props>) => act(() => { view.update(<Probe {...props} {...next} />); Object.assign(props, next); });
}

describe("useWakeOnShow", () => {
	it("starts a sleeping device once it is shown, and only once", () => {
		const start = vi.fn();
		const update = mount({ asleep: true, shown: true, starting: false, start });
		expect(start).toHaveBeenCalledTimes(1);
		update({ starting: true });
		update({ starting: false, start: vi.fn(() => start()) });
		expect(start).toHaveBeenCalledTimes(1);
	});

	it("leaves it asleep while hidden, and tries again each time it is shown", () => {
		const start = vi.fn();
		const update = mount({ asleep: true, shown: false, starting: false, start });
		expect(start).not.toHaveBeenCalled();
		update({ shown: true });
		expect(start).toHaveBeenCalledTimes(1);
		update({ shown: false });
		update({ shown: true });
		expect(start).toHaveBeenCalledTimes(2);
	});

	it("starts it again after a later sleep", () => {
		const start = vi.fn();
		const update = mount({ asleep: true, shown: true, starting: false, start });
		update({ asleep: false });
		update({ asleep: true });
		expect(start).toHaveBeenCalledTimes(2);
	});

	it("never starts a device that is not asleep", () => {
		const start = vi.fn();
		mount({ asleep: false, shown: true, starting: false, start });
		expect(start).not.toHaveBeenCalled();
	});
});
