import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({
	BackHandler: { addEventListener: () => ({ remove: () => {} }) },
	Keyboard: { addListener: () => ({ remove: () => {} }) },
}));
vi.mock("./effort-gauge", () => ({ EffortGaugeButton: "EffortGaugeButton" }));
vi.mock("./effort-slider", () => ({ EffortSliderOverlay: "EffortSliderOverlay" }));

import { EffortSliderGauge, EffortSliderHost } from "./effort-slider-host";
import { createEffortSliderSwitch, type EffortSliderSwitch } from "./useEffortSlider";

const levels = [{ value: "low", label: "Low", selected: true }, { value: "high", label: "High", selected: false }];
const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

// A composer that counts its own renders, with the gauge and the slider in it.
function render(slider: EffortSliderSwitch, over: { busy?: boolean; onPress?: () => void; choices?: typeof levels; ultracode?: { on: boolean; toggle: () => void } } = {}) {
	const counts = { composer: 0 };
	const onChoose = vi.fn();
	const choices = over.choices ?? levels;
	function Composer({ busy }: { busy: boolean }) {
		counts.composer += 1;
		return (
			<>
				<EffortSliderGauge slider={slider} choices={choices} toggles={over.ultracode ? { ultracode: over.ultracode.on } : undefined} disabled={busy} onPress={over.onPress} testID="gauge" />
				<EffortSliderHost slider={slider} choices={choices} busy={busy} onChoose={onChoose} ultracode={over.ultracode} />
			</>
		);
	}
	act(() => { renderer = create(<Composer busy={over.busy ?? false} />); });
	const root = renderer!.root;
	return {
		counts,
		gauge: () => root.findAll((node) => hostType(node) === "EffortGaugeButton")[0],
		overlay: () => root.findAll((node) => hostType(node) === "EffortSliderOverlay")[0],
		setBusy: (busy: boolean) => act(() => renderer!.update(<Composer busy={busy} />)),
	};
}

describe("effort slider host", () => {
	// Re-rendering a whole chat composer on each open held the slider back by
	// half a second on a phone.
	it("opens and closes from the gauge without re-rendering the composer", () => {
		const slider = createEffortSliderSwitch();
		slider.setBottom(120);
		const onPress = vi.fn();
		const { counts, gauge, overlay } = render(slider, { onPress });
		const before = counts.composer;
		act(() => gauge().props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
		expect(gauge().props.open).toBe(true);
		expect(overlay().props.bottom).toBe(120);
		act(() => gauge().props.onPress());
		expect(gauge().props.open).toBe(false);
		expect(overlay()).toBeUndefined();
		expect(counts.composer).toBe(before);
	});

	it("floats at the height the composer last reported, without re-rendering it", () => {
		const slider = createEffortSliderSwitch();
		const { counts, gauge, overlay } = render(slider);
		act(() => gauge().props.onPress());
		// Nothing to float above yet.
		expect(overlay()).toBeUndefined();
		const before = counts.composer;
		act(() => slider.setBottom(96));
		expect(overlay().props.bottom).toBe(96);
		act(() => slider.setBottom(140));
		expect(overlay().props.bottom).toBe(140);
		expect(counts.composer).toBe(before);
	});

	// Built once the screen is idle, so the tap only reveals it.
	it("builds the slider hidden once the screen is idle, and keeps it between opens", () => {
		const idle: (() => void)[] = [];
		vi.stubGlobal("requestIdleCallback", (fn: () => void) => idle.push(fn));
		vi.stubGlobal("cancelIdleCallback", () => {});
		const slider = createEffortSliderSwitch();
		slider.setBottom(120);
		const { gauge, overlay } = render(slider);
		expect(overlay()).toBeUndefined();
		act(() => idle.forEach((fn) => fn()));
		expect(overlay().props.visible).toBe(false);
		act(() => gauge().props.onPress());
		expect(overlay().props.visible).toBe(true);
		act(() => gauge().props.onPress());
		expect(overlay().props.visible).toBe(false);
	});

	// Busy only keeps it from opening: a slider in use stays up under the finger.
	it("keeps an open slider up while busy, the gauge disabled but lit", () => {
		const slider = createEffortSliderSwitch();
		slider.setBottom(120);
		const { gauge, overlay, setBusy } = render(slider);
		act(() => gauge().props.onPress());
		expect(overlay()).toBeDefined();
		setBusy(true);
		expect(gauge().props.disabled).toBe(true);
		expect(gauge().props.open).toBe(true);
		expect(overlay()).toBeDefined();
	});

	// Claude Haiku takes no effort: the slider still opens for Ultracode, and
	// only for a switch.
	it("opens with no levels while the slider carries Ultracode", () => {
		const slider = createEffortSliderSwitch();
		slider.setBottom(120);
		const ultracode = { on: false, toggle: () => {} };
		const { gauge, overlay } = render(slider, { choices: [], ultracode });
		act(() => gauge().props.onPress());
		expect(gauge().props.open).toBe(true);
		expect(overlay().props).toMatchObject({ visible: true, choices: [], ultracode });
		act(() => renderer!.unmount());
		renderer = undefined;
		const bare = createEffortSliderSwitch();
		bare.setBottom(120);
		const none = render(bare, { choices: [] });
		act(() => none.gauge().props.onPress());
		expect(none.gauge().props.open).toBe(false);
		expect(none.overlay()).toBeUndefined();
	});

	// A request card takes the composer's place, host and all; dismissing it
	// must not bring the slider back with no tap.
	it("closes the composer's switch when the host goes away", () => {
		const slider = createEffortSliderSwitch();
		slider.setBottom(120);
		const { gauge } = render(slider);
		act(() => gauge().props.onPress());
		expect(slider.isOpen()).toBe(true);
		act(() => renderer!.update(<></>));
		expect(slider.isOpen()).toBe(false);
	});
});
