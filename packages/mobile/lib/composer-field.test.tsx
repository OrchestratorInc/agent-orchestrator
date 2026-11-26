import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ setSelection: vi.fn() }));

vi.mock("react-native", async () => {
	const React = await import("react");
	class TextInput extends React.Component<Record<string, unknown>> {
		setSelection = fixtures.setSelection;
		render() { return React.createElement("TextInput", this.props); }
	}
	return { TextInput };
});

vi.mock("./ThemeProvider", () => ({ useTheme: () => ({}) }));
vi.mock("./textSelection", () => ({ textSelectionColors: () => ({ selectionColor: "#selection" }) }));

import { ComposerField } from "./composer-field";
import { createEffortSliderSwitch } from "./useEffortSlider";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixtures.setSelection.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

const field = (): ReactTestInstance => renderer!.root.findAll((node) => (node.type as unknown) === "TextInput")[0];
const select = (start: number, end: number) => act(() => field().props.onSelectionChange({ nativeEvent: { selection: { start, end } } }));

// Android draws the caret's drop and a selection's handles in a window above
// the app, over any overlay; they step aside while one is open.
describe("composer field", () => {
	it("hides its caret while an overlay is open, and brings it back", () => {
		const slider = createEffortSliderSwitch();
		const picker = createEffortSliderSwitch();
		const overlays = [slider, picker];
		act(() => { renderer = create(<ComposerField overlays={overlays} />); });
		expect(field().props.caretHidden).toBeFalsy();
		act(() => slider.setOpen(true));
		expect(field().props.caretHidden).toBe(true);
		act(() => { slider.setOpen(false); picker.setOpen(true); });
		expect(field().props.caretHidden).toBe(true);
		act(() => picker.setOpen(false));
		expect(field().props.caretHidden).toBeFalsy();
	});

	it("collapses a selection to its end as an overlay opens, and leaves a bare caret alone", () => {
		const slider = createEffortSliderSwitch();
		const overlays = [slider];
		const onSelectionChange = vi.fn();
		act(() => { renderer = create(<ComposerField overlays={overlays} onSelectionChange={onSelectionChange} />); });
		select(3, 3);
		act(() => slider.setOpen(true));
		expect(fixtures.setSelection).not.toHaveBeenCalled();
		act(() => slider.setOpen(false));
		select(2, 7);
		expect(onSelectionChange).toHaveBeenCalledTimes(2);
		act(() => slider.setOpen(true));
		expect(fixtures.setSelection).toHaveBeenCalledExactlyOnceWith(7, 7);
	});

	it("puts an open overlay away as a key is typed, and still takes the key", () => {
		const slider = createEffortSliderSwitch();
		const onChangeText = vi.fn();
		const overlays = [slider];
		act(() => { renderer = create(<ComposerField overlays={overlays} onChangeText={onChangeText} />); });
		act(() => slider.setOpen(true));
		act(() => field().props.onChangeText("h"));
		expect(slider.isOpen()).toBe(false);
		expect(onChangeText).toHaveBeenCalledExactlyOnceWith("h");
	});

	it("hands the field to its owner's ref", () => {
		const ref = { current: null as unknown };
		act(() => { renderer = create(<ComposerField overlays={[]} ref={ref as never} />); });
		expect(ref.current).not.toBeNull();
	});
});
