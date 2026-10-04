import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

// React Native's own Jest setup (packages/jest-preset/jest/setup.js) sets both:
// the first lets act() flush without warnings, the second silences
// react-test-renderer's deprecation warning in a React Native test environment.
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { haptics, hosts, Slider } = vi.hoisted(() => ({
	haptics: { select: vi.fn() },
	/** How many native slider hosts have mounted; a remount rebuilds the Compose slider. */
	hosts: { mounted: 0 },
	Slider: "Slider",
}));

vi.mock("@expo/ui", async () => {
	const { createElement, useEffect } = await import("react");
	function Host({ children }: { children?: unknown }) {
		useEffect(() => { hosts.mounted += 1; }, []);
		return createElement("Host", null, children as never);
	}
	return { Host, Slider, Switch: "Switch" };
});
vi.mock("react-native", () => ({
	ActivityIndicator: "ActivityIndicator",
	Pressable: "Pressable",
	ScrollView: "ScrollView",
	StyleSheet: { create: (styles: unknown) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("../icons", () => ({ Feather: "Feather" }));
vi.mock("../haptics", () => ({ haptics }));
vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ accent: "blue", textSecondary: "gray" }),
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({ accent: "blue", textSecondary: "gray" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("../ui", () => ({ SheetHeader: "SheetHeader" }));

import { EffortSlider } from "./ChatSettingsModal.android";

const choices = [
	{ value: "low", label: "Low" },
	{ value: "medium", label: "Medium" },
	{ value: "high", label: "High" },
];

const settled = () => vi.fn((_value: string) => Promise.resolve());

/** A write the test answers by hand, as the sheet does once the daemon replies. */
function pendingWrite() {
	let answer!: () => void;
	const promise = new Promise<void>((resolve) => { answer = resolve; });
	return { promise, answer };
}

function slider(renderer: ReactTestRenderer) {
	return renderer.root.findByProps({ testID: "turn-settings-effort" });
}

/** What the user sees: the thumb's position and the level named in the header. */
function shown(renderer: ReactTestRenderer) {
	return { value: slider(renderer).props.value, label: renderer.root.findByProps({ testID: "turn-settings-effort-value" }).props.children };
}

describe("EffortSlider", () => {
	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
		haptics.select.mockClear();
		hosts.mounted = 0;
	});

	it("does not write when an unlisted effort is mounted or refreshed", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		act(() => {
			vi.advanceTimersByTime(300);
		});
		expect(onChange).not.toHaveBeenCalled();

		act(() => {
			renderer.update(<EffortSlider choices={[...choices]} selected="ultra" unplaced="Ultra" onChange={onChange} />);
		});
		act(() => {
			vi.advanceTimersByTime(300);
		});
		expect(onChange).not.toHaveBeenCalled();

		act(() => renderer.unmount());
	});

	it("writes a level once only after the user moves the slider", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => {
			vi.advanceTimersByTime(179);
		});
		expect(onChange).not.toHaveBeenCalled();

		act(() => {
			vi.advanceTimersByTime(1);
		});
		expect(onChange).toHaveBeenCalledOnce();
		expect(onChange).toHaveBeenCalledWith("high");
		expect(haptics.select).toHaveBeenCalledOnce();

		act(() => {
			vi.advanceTimersByTime(300);
		});
		expect(onChange).toHaveBeenCalledOnce();
		act(() => renderer.unmount());
	});

	it("does not retry a failed write when the sheet rerenders", async () => {
		vi.useFakeTimers();
		const onChange = settled();
		const afterFailure = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		await act(async () => { vi.advanceTimersByTime(180); });
		expect(onChange).toHaveBeenCalledOnce();
		expect(onChange).toHaveBeenCalledWith("high");

		// The server rejected the change. The route shows an error and keeps the
		// old selected value, so a new render must not start another write.
		act(() => {
			renderer.update(<EffortSlider choices={[...choices]} selected="default" unplaced="Not reported" onChange={afterFailure} />);
		});
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).toHaveBeenCalledOnce();
		expect(afterFailure).not.toHaveBeenCalled();
		act(() => renderer.unmount());
	});

	it("keeps a user's pending choice through an unrelated sheet rerender", () => {
		vi.useFakeTimers();
		const oldCallback = settled();
		const latestCallback = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={oldCallback} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(100); });
		act(() => {
			renderer.update(<EffortSlider choices={[...choices]} selected="default" unplaced="Not reported" onChange={latestCallback} />);
		});
		act(() => { vi.advanceTimersByTime(80); });
		expect(oldCallback).not.toHaveBeenCalled();
		expect(latestCallback).toHaveBeenCalledOnce();
		expect(latestCallback).toHaveBeenCalledWith("high");
		act(() => renderer.unmount());
	});

	it("goes back to the saved level when the write is rejected", async () => {
		vi.useFakeTimers();
		const write = pendingWrite();
		const onChange = vi.fn((_value: string) => write.promise);
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		expect(shown(renderer)).toEqual({ value: 2, label: "High" });
		act(() => { vi.advanceTimersByTime(180); });
		expect(onChange).toHaveBeenCalledWith("high");

		// The route disables the sheet while it saves; the move stays on screen.
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" disabled onChange={onChange} />);
		});
		expect(shown(renderer)).toEqual({ value: 2, label: "High" });

		// Rejected: the route re-enables the sheet with the old value and settles.
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		expect(hosts.mounted).toBe(1);
		await act(async () => write.answer());
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		// The Compose slider keeps a dragged thumb under a held finger whatever
		// value it is given, so it is rebuilt on the saved level.
		expect(hosts.mounted).toBe(2);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).toHaveBeenCalledOnce();
		act(() => renderer.unmount());
	});

	it("goes back to the unplaced label when a write from an unlisted effort is rejected", async () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(1));
		await act(async () => { vi.advanceTimersByTime(180); });
		expect(onChange).toHaveBeenCalledWith("medium");
		expect(shown(renderer)).toEqual({ value: 0, label: "Not reported" });
		expect(hosts.mounted).toBe(2);
		act(() => renderer.unmount());
	});

	it("shows the saved level after the write succeeds", async () => {
		vi.useFakeTimers();
		const write = pendingWrite();
		const onChange = vi.fn((_value: string) => write.promise);
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(180); });
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" disabled onChange={onChange} />);
		});
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="high" unplaced="High" onChange={onChange} />);
		});
		await act(async () => write.answer());
		expect(shown(renderer)).toEqual({ value: 2, label: "High" });
		// The thumb is already where the user left it: nothing to rebuild.
		expect(hosts.mounted).toBe(1);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).toHaveBeenCalledOnce();
		act(() => renderer.unmount());
	});

	it("drops a move from the screen when the sheet disables the slider before it is sent", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(100); });
		// A refresh starts before the 180 ms pause ends.
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" disabled onChange={onChange} />);
		});
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		expect(hosts.mounted).toBe(2);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).not.toHaveBeenCalled();
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		expect(hosts.mounted).toBe(2);
		act(() => renderer.unmount());
	});

	it("shows a newly saved level in place of a move that was not sent", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(100); });
		act(() => {
			renderer.update(<EffortSlider choices={choices} selected="medium" unplaced="Medium" onChange={onChange} />);
		});
		expect(shown(renderer)).toEqual({ value: 1, label: "Medium" });
		expect(hosts.mounted).toBe(2);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).not.toHaveBeenCalled();
		act(() => renderer.unmount());
	});

	it("drops the move when the user slides back onto the saved level", () => {
		vi.useFakeTimers();
		const onChange = settled();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(100); });
		act(() => slider(renderer).props.onValueChange(0));
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		// The finger is on the saved level, so the slider it is dragging stays.
		expect(hosts.mounted).toBe(1);
		act(() => { vi.advanceTimersByTime(500); });
		expect(onChange).not.toHaveBeenCalled();
		act(() => renderer.unmount());
	});

	it("keeps a newer move on screen when an older write settles first", async () => {
		vi.useFakeTimers();
		const first = pendingWrite();
		const second = pendingWrite();
		const onChange = vi.fn((_value: string) => Promise.resolve())
			.mockImplementationOnce(() => first.promise)
			.mockImplementationOnce(() => second.promise);
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="low" unplaced="Low" onChange={onChange} />);
		});
		act(() => slider(renderer).props.onValueChange(1));
		act(() => { vi.advanceTimersByTime(180); });
		// The thumb moved again before the sheet disabled it for the first save.
		act(() => slider(renderer).props.onValueChange(2));
		act(() => { vi.advanceTimersByTime(180); });
		expect(onChange.mock.calls).toEqual([["medium"], ["high"]]);

		await act(async () => first.answer());
		expect(shown(renderer)).toEqual({ value: 2, label: "High" });
		expect(hosts.mounted).toBe(1);
		await act(async () => second.answer());
		expect(shown(renderer)).toEqual({ value: 0, label: "Low" });
		expect(hosts.mounted).toBe(2);
		act(() => renderer.unmount());
	});
});
