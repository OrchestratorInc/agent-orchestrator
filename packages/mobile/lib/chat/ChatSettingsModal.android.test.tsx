import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

const { haptics, Slider } = vi.hoisted(() => ({
	haptics: { select: vi.fn() },
	Slider: "Slider",
}));

vi.mock("@expo/ui", () => ({ Host: "Host", Slider, Switch: "Switch" }));
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

describe("EffortSlider", () => {
	afterEach(() => {
		vi.useRealTimers();
		vi.restoreAllMocks();
	});

	it("does not write when an unlisted effort is mounted or refreshed", () => {
		vi.useFakeTimers();
		const onChange = vi.fn();
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
		const onChange = vi.fn();
		let renderer!: ReactTestRenderer;

		act(() => {
			renderer = create(<EffortSlider choices={choices} selected="default" unplaced="Not reported" onChange={onChange} />);
		});
		const slider = renderer.root.findByProps({ testID: "turn-settings-effort" });
		act(() => slider.props.onValueChange(2));
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
});
