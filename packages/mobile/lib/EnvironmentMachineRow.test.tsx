import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { MachineAvatar } from "./MachineIdentity";
import { useState } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import type { Host } from "./hosts";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true, IS_REACT_NATIVE_TEST_ENVIRONMENT: true });

const { alert, haptics, animation } = vi.hoisted(() => ({
	alert: vi.fn(),
	haptics: { tap: vi.fn(), select: vi.fn(), success: vi.fn(), warning: vi.fn() },
	animation: { reduceMotion: false },
}));

vi.mock("react-native", () => ({
	Animated: { View: "NativeAnimatedView" },
	ActivityIndicator: "ActivityIndicator",
	Alert: { alert },
	Keyboard: { dismiss: vi.fn() },
	Platform: { OS: "ios" },
	Pressable: "Pressable",
	StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1 },
	Switch: "Switch",
	Text: "Text",
	TextInput: "TextInput",
	View: "View",
}));
vi.mock("react-native-reanimated", () => ({
	default: { View: "AnimatedView" },
	Easing: { bezier: (...curve: number[]) => ({ curve }) },
	FadeIn: { duration: (duration: number) => ({ kind: "fade-in", duration }) },
	FadeOut: { duration: (duration: number) => ({ kind: "fade-out", duration }) },
	LinearTransition: {
		duration: (duration: number) => ({
			kind: "layout",
			duration,
			easing(easing: unknown) { return { ...this, easing }; },
		}),
	},
}));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("react-native-gesture-handler/Swipeable", async () => {
	const React = await import("react");
	return {
		default: React.forwardRef(function Swipeable({ children, renderRightActions, ...props }: { children?: React.ReactNode; renderRightActions?: (progress: unknown) => React.ReactNode }, ref) {
			React.useImperativeHandle(ref, () => ({ close: vi.fn() }));
			const progress = { interpolate: ({ inputRange, outputRange }: { inputRange: number[]; outputRange: number[] }) => ({
				valueAt(value: number) {
					if (value <= inputRange[0]) return outputRange[0];
					if (value >= inputRange[1]) return outputRange[1];
					return outputRange[0] + (outputRange[1] - outputRange[0]) * (value - inputRange[0]) / (inputRange[1] - inputRange[0]);
				},
			}) };
			return React.createElement("Swipeable", props, renderRightActions?.(progress), children);
		}),
	};
});
vi.mock("react-native-svg", () => ({ default: "Svg", Path: "Path", Rect: "Rect" }));
vi.mock("./haptics", () => ({ haptics }));
vi.mock("./useReducedMotion", () => ({ useReducedMotion: () => animation.reduceMotion }));
vi.mock("./ThemeProvider", () => {
	const theme = {
		accent: "blue", bgBase: "base", bgElevated: "elevated", bgElevatedHover: "hover", bgSubtle: "subtle",
		borderDefault: "border", borderStrong: "strong", borderSubtle: "divider", green: "green", onAccent: "black",
		red: "red", textFaint: "faint", textPrimary: "white", textSecondary: "gray", textTertiary: "dim",
		tintGreen: "green-tint", tintRed: "red-tint", fontMono: "mono",
	};
	return {
		useTheme: () => theme,
		useThemedStyles: (factory: (value: typeof theme) => unknown) => factory(theme),
	};
});

import { EnvironmentMachineRow, EnvironmentEditorActions, type EnvironmentEditorState } from "./EnvironmentMachineRow";

const host: Host = {
	id: "host-1",
	name: "Studio Mac",
	platform: "darwin",
	token: "secret",
	lastConnected: 1,
	endpoints: [
		{ kind: "lan", host: "192.168.1.24", port: 3011, secure: false },
		{ kind: "tailscale", host: "studio.tail123.ts.net", port: 3011, secure: true },
	],
};

const activeConfig: ServerConfig = {
	host: "studio.tail123.ts.net",
	httpPort: "3011",
	muxPort: "3012",
	secure: true,
	password: "secret",
	hostId: "host-1",
	endpointKind: "tailscale",
};

const secondHost: Host = {
	...host,
	id: "host-2",
	name: "Dev",
	endpoints: [{ kind: "lan", host: "192.168.1.25", port: 3011, secure: false }],
};

type PlannedProps = Partial<React.ComponentProps<typeof EnvironmentMachineRow>> & {
	expanded?: boolean;
	onExpandedChange?: (expanded: boolean) => void;
	forgetDisabled?: boolean;
	forgetting?: boolean;
	onForget?: () => Promise<void>;
};

type AccordionRowProps = React.ComponentProps<typeof EnvironmentMachineRow> & {
	expanded: boolean;
	onExpandedChange: (expanded: boolean) => void;
};

function AccordionHarness() {
	const [expandedHostId, setExpandedHostId] = useState<string | null>(host.id);
	const PlannedEnvironmentMachineRow = EnvironmentMachineRow as React.ComponentType<AccordionRowProps>;
	return <>
		{[host, secondHost].map((machine) => (
			<PlannedEnvironmentMachineRow
				key={machine.id}
				host={machine}
				activeConfig={machine.id === host.id ? activeConfig : undefined}
				isDefault={machine.id === host.id}
				expanded={expandedHostId === machine.id}
				onExpandedChange={(expanded) => setExpandedHostId(expanded ? machine.id : null)}
				status={{ label: "Connected", color: "green", tint: "green-tint" }}
				switching={false}
				switchingDisabled={false}
				onMakeDefault={vi.fn()}
				onReconnect={vi.fn()}
				onSave={vi.fn(async () => {})}
				forgetDisabled={false}
				forgetting={false}
				onForget={vi.fn(async () => {})}
			/>
		))}
	</>;
}

function render(onSave = vi.fn(async () => {}), overrides: PlannedProps = {}) {
	const { expanded: initialExpanded, onExpandedChange, ...rowOverrides } = overrides;
	function ControlledRow() {
		const [expanded, setExpanded] = useState(initialExpanded ?? rowOverrides.isDefault ?? true);
		return <EnvironmentMachineRow
			host={host}
			activeConfig={activeConfig}
			isDefault
			expanded={expanded}
			onExpandedChange={(nextExpanded) => {
				setExpanded(nextExpanded);
				onExpandedChange?.(nextExpanded);
			}}
			status={{ label: "Connected", color: "green", tint: "green-tint" }}
			switching={false}
			switchingDisabled={false}
			onMakeDefault={vi.fn()}
			onReconnect={vi.fn()}
			onSave={onSave}
			forgetDisabled={rowOverrides.forgetDisabled ?? false}
			forgetting={rowOverrides.forgetting ?? false}
			onForget={rowOverrides.onForget ?? vi.fn(async () => {})}
			{...rowOverrides}
		/>;
	}
	let renderer!: ReactTestRenderer;
	act(() => {
		renderer = create(<ControlledRow />);
	});
	return { renderer, onSave };
}

describe("EnvironmentMachineRow", () => {
	beforeEach(() => {
		animation.reduceMotion = false;
	});

	it("publishes editing actions outside the card and saves the latest draft from the fixed footer", async () => {
		const onSave = vi.fn(async () => {});
		function Screen() {
			const [editor, setEditor] = useState<EnvironmentEditorState | null>(null);
			const props = {
				host, activeConfig, isDefault: true, expanded: true, onExpandedChange: vi.fn(),
				status: { label: "Connected", color: "green", tint: "green-tint" },
				switching: false, switchingDisabled: false, onMakeDefault: vi.fn(), onReconnect: vi.fn(),
				onSave, forgetDisabled: false, forgetting: false, onForget: vi.fn(async () => {}),
			};
			return <>
				<EnvironmentMachineRow {...props} onEditorStateChange={setEditor} />
				{editor ? <EnvironmentEditorActions editor={editor} /> : null}
			</>;
		}
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<Screen />); });
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());
		const row = renderer.root.findByProps({ testID: "environment-machine-host-1" });
		expect(row.findAllByProps({ accessibilityLabel: "Save machine changes" })).toHaveLength(0);
		expect(renderer.root.findByProps({ accessibilityLabel: "Cancel editing machine" })).toBeTruthy();
		act(() => renderer.root.findByProps({ testID: "environment-name-host-1" }).props.onChangeText(""));
		expect(renderer.root.findByProps({ testID: "environment-save-host-1" }).props.disabled).toBe(true);
		act(() => renderer.root.findByProps({ testID: "environment-name-host-1" }).props.onChangeText("Travel Mac"));
		expect(renderer.root.findByProps({ testID: "environment-save-host-1" }).props.disabled).toBe(false);
		await act(async () => renderer.root.findByProps({ testID: "environment-save-host-1" }).props.onPress());
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ machineName: "Travel Mac" }));
		expect(renderer.root.findAllByProps({ accessibilityLabel: "Save machine changes" })).toHaveLength(0);
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());
		act(() => renderer.root.findByProps({ accessibilityLabel: "Cancel editing machine" }).props.onPress());
		expect(renderer.root.findAllByProps({ testID: "environment-editor-host-1" })).toHaveLength(0);
		expect(onSave).toHaveBeenCalledOnce();
		act(() => renderer.unmount());
	});

	it("moves the swipe rail and card together during accordion changes", () => {
		const { renderer } = render();
		const layoutShell = renderer.root.findByProps({ testID: "environment-layout-host-1" });
		const row = renderer.root.findByProps({ testID: "environment-machine-host-1" });
		const details = renderer.root.findByProps({ testID: "environment-details-host-1" });

		expect(layoutShell.type).toBe("AnimatedView");
		expect(layoutShell.props.layout).toEqual(expect.objectContaining({ kind: "layout", duration: 160 }));
		expect(row.type).toBe("View");
		expect(row.props.layout).toBeUndefined();
		expect(details.type).toBe("AnimatedView");
		expect(details.props.entering).toEqual({ kind: "fade-in", duration: 120 });
		expect(details.props.exiting).toEqual({ kind: "fade-out", duration: 120 });
	});

	it("disables accordion motion when Reduce Motion is enabled", () => {
		animation.reduceMotion = true;
		const { renderer } = render();
		const layoutShell = renderer.root.findByProps({ testID: "environment-layout-host-1" });
		const details = renderer.root.findByProps({ testID: "environment-details-host-1" });

		expect(layoutShell.props.layout).toBeUndefined();
		expect(details.props.entering).toBeUndefined();
		expect(details.props.exiting).toBeUndefined();
	});

	it("closes the previously expanded machine when another machine opens", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<AccordionHarness />); });

		expect(renderer.root.findAllByProps({ testID: "environment-details-host-1" })).toHaveLength(1);
		expect(renderer.root.findAllByProps({ testID: "environment-details-host-2" })).toHaveLength(0);

		act(() => renderer.root.findByProps({ testID: "environment-summary-host-2" }).props.onPress());

		expect(renderer.root.findAllByProps({ testID: "environment-details-host-1" })).toHaveLength(0);
		expect(renderer.root.findAllByProps({ testID: "environment-details-host-2" })).toHaveLength(1);
	});

	it("cancels the previous machine editor when another machine opens", () => {
		let renderer!: ReactTestRenderer;
		act(() => { renderer = create(<AccordionHarness />); });

		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());
		expect(renderer.root.findAllByProps({ testID: "environment-editor-host-1" })).toHaveLength(1);

		act(() => renderer.root.findByProps({ testID: "environment-summary-host-2" }).props.onPress());
		expect(renderer.root.findAllByProps({ testID: "environment-editor-host-1" })).toHaveLength(0);

		act(() => renderer.root.findByProps({ testID: "environment-edit-host-2" }).props.onPress());
		expect(renderer.root.findAllByProps({ testID: "environment-editor-host-2" })).toHaveLength(1);
	});

	it("reveals the action without opening confirmation on a swipe", () => {
		alert.mockClear();
		const { renderer } = render(undefined, { isDefault: false });
		const swipeable = renderer.root.findByType("Swipeable" as unknown as React.ElementType);

		expect(swipeable.props.enabled).toBe(true);
		act(() => swipeable.props.onSwipeableWillOpen?.("right"));
		expect(alert).not.toHaveBeenCalled();
	});

	it("keeps the red underlay hidden at rest and reveals it only during a swipe", () => {
		const { renderer } = render(undefined, { isDefault: false });
		const swipeable = renderer.root.findByType("Swipeable" as unknown as React.ElementType);
		const row = renderer.root.findByProps({ testID: "environment-machine-host-1" });

		expect(swipeable.props.containerStyle.backgroundColor).not.toBe("red");
		expect(swipeable.props.containerStyle.overflow).toBe("hidden");
		expect(swipeable.props.childrenContainerStyle).toBeUndefined();
		expect(row.props.style[0]).toEqual(expect.objectContaining({
			backgroundColor: "elevated",
			borderColor: "border",
		}));
		const underlay = renderer.root.findByProps({ testID: "environment-forget-underlay-host-1" });
		expect(underlay.props.style[0]).toEqual(expect.objectContaining({
			position: "absolute", top: 0, bottom: 0, left: 0, right: 0, backgroundColor: "red",
		}));
		const interpolation = underlay.props.style[1].opacity;
		expect(interpolation.valueAt(0)).toBe(0);
		expect(interpolation.valueAt(0.5)).toBe(1);
		expect(interpolation.valueAt(1)).toBe(1);
		expect(interpolation.valueAt(0)).toBe(0);
	});

	it("asks for confirmation before forgetting a swiped machine", async () => {
		const onForget = vi.fn(async () => {});
		const { renderer } = render(undefined, { isDefault: false, onForget });

		act(() => renderer.root.findByProps({ accessibilityLabel: "Forget Studio Mac" }).props.onPress());

		expect(onForget).not.toHaveBeenCalled();
		expect(alert).toHaveBeenCalledWith(
			"Forget Studio Mac?",
			expect.stringMatching(/saved connection from this phone[\s\S]*workers and files[\s\S]*pair[\s\S]*reachable/i),
			expect.any(Array),
		);
		const buttons = alert.mock.calls.at(-1)?.[2] as { text: string; onPress?: () => void }[];
		const confirm = buttons.find((button) => button.text === "Forget");
		await act(async () => confirm?.onPress?.());

		expect(onForget).toHaveBeenCalledOnce();
	});

	it("disables expanded-card swiping but offers an explicit Forget machine action", () => {
		alert.mockClear();
		const onForget = vi.fn(async () => {});
		const { renderer } = render(undefined, { onForget });
		expect(renderer.root.findByType("Swipeable" as never).props.enabled).toBe(false);
		act(() => renderer.root.findByProps({ testID: "environment-forget-action-host-1" }).props.onPress());
		const buttons = alert.mock.calls.at(-1)?.[2] as { text: string; style?: string; onPress?: () => void }[];
		expect(buttons.find((button) => button.text === "Forget")?.style).toBe("destructive");
		act(() => buttons.find((button) => button.text === "Cancel")?.onPress?.());
		expect(onForget).not.toHaveBeenCalled();
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());
		expect(renderer.root.findAllByProps({ testID: "environment-forget-action-host-1" })).toHaveLength(0);
	});

	it.each([{ forgetting: true }, { forgetDisabled: true }])("prevents removal while another action is busy: %j", (busy) => {
		alert.mockClear();
		const { renderer } = render(undefined, busy);
		const remove = renderer.root.findByProps({ testID: "environment-forget-action-host-1" });
		expect(remove.props.disabled).toBe(true);
		act(() => remove.props.onPress());
		expect(alert).not.toHaveBeenCalled();
		expect(renderer.root.findByType("Swipeable" as never).props.enabled).toBe(false);
	});

	it("keeps non-default machines compact until their summary is opened", () => {
		const { renderer } = render(undefined, { isDefault: false });
		const summary = renderer.root.findByProps({ testID: "environment-summary-host-1" });

		expect(summary.props.accessibilityState).toEqual({ expanded: false });
		expect(renderer.root.findAllByProps({ testID: "environment-details-host-1" })).toHaveLength(0);
		expect(renderer.root.findAllByProps({ testID: "environment-edit-host-1" })).toHaveLength(0);

		act(() => summary.props.onPress());

		expect(renderer.root.findByProps({ testID: "environment-summary-host-1" }).props.accessibilityState).toEqual({ expanded: true });
		expect(renderer.root.findByProps({ testID: "environment-details-host-1" })).toBeTruthy();
		expect(renderer.root.findByProps({ testID: "environment-edit-host-1" })).toBeTruthy();
	});

	it("keeps expansion controlled when a machine becomes the default", () => {
		const { renderer } = render(undefined, { isDefault: false });
		const props = renderer.root.findByType(EnvironmentMachineRow).props as React.ComponentProps<typeof EnvironmentMachineRow>;

		expect(renderer.root.findByProps({ testID: "environment-summary-host-1" }).props.accessibilityState).toEqual({ expanded: false });

		act(() => renderer.update(<EnvironmentMachineRow {...props} isDefault expanded={false} />));

		expect(renderer.root.findByProps({ testID: "environment-summary-host-1" }).props.accessibilityState).toEqual({ expanded: false });
		expect(renderer.root.findAllByProps({ testID: "environment-details-host-1" })).toHaveLength(0);

		act(() => renderer.update(<EnvironmentMachineRow {...props} isDefault expanded />));

		expect(renderer.root.findByProps({ testID: "environment-summary-host-1" }).props.accessibilityState).toEqual({ expanded: true });
		expect(renderer.root.findByProps({ testID: "environment-details-host-1" })).toBeTruthy();
	});

	it("matches the compact route hierarchy and reveals secondary routes on demand", () => {
		const { renderer } = render();
		const otherRoutes = renderer.root.findByProps({ testID: "environment-other-routes-host-1" });

		expect(renderer.root.findByProps({ testID: "environment-route-summary-host-1" }).props.children).toBe("Tailscale · studio.tail123.ts.net");
		expect(renderer.root.findByProps({ testID: "environment-active-address-host-1" }).props.children).toBe("studio.tail123.ts.net:3011");
		expect(otherRoutes.props.accessibilityState).toEqual({ expanded: false });
		expect(renderer.root.findAllByProps({ testID: "environment-other-route-lan://192.168.1.24:3011" })).toHaveLength(0);

		act(() => otherRoutes.props.onPress());

		expect(renderer.root.findByProps({ testID: "environment-other-routes-host-1" }).props.accessibilityState).toEqual({ expanded: true });
		expect(renderer.root.findByProps({ testID: "environment-other-route-lan://192.168.1.24:3011" })).toBeTruthy();
	});

	it("expands the editor as an accordion without replacing the machine actions", () => {
		const { renderer } = render();
		const edit = renderer.root.findByProps({ testID: "environment-edit-host-1" });

		expect(edit.props.accessibilityState).toEqual({ expanded: false });
		expect(renderer.root.findByProps({ testID: "environment-reconnect-host-1" })).toBeTruthy();

		act(() => edit.props.onPress());

		expect(renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.accessibilityState).toEqual({ expanded: true });
		expect(renderer.root.findByProps({ testID: "environment-reconnect-host-1" })).toBeTruthy();
		expect(renderer.root.findByProps({ testID: "environment-editor-host-1" })).toBeTruthy();

		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());

		expect(renderer.root.findAllByProps({ testID: "environment-editor-host-1" })).toHaveLength(0);
		expect(renderer.root.findByProps({ testID: "environment-machine-host-1" })).toBeTruthy();
	});

	it("edits the active route inside the machine row", async () => {
		const { renderer, onSave } = render();
		expect(renderer.root.findAllByProps({ testID: "environment-editor-host-1" })).toHaveLength(0);

		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());

		expect(renderer.root.findByProps({ testID: "environment-editor-host-1" })).toBeTruthy();
		expect(renderer.root.findByProps({ testID: "environment-name-host-1" }).props.value).toBe("Studio Mac");
		expect(renderer.root.findByProps({ testID: "environment-host-host-1" }).props.value).toBe("studio.tail123.ts.net");

		act(() => renderer.root.findByProps({ testID: "environment-name-host-1" }).props.onChangeText("Travel Mac"));
		act(() => renderer.root.findByProps({ testID: "environment-host-host-1" }).props.onChangeText("travel.tail123.ts.net"));
		await act(async () => renderer.root.findByProps({ testID: "environment-save-host-1" }).props.onPress());

		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			machineName: "Travel Mac",
			endpointIndex: 1,
			config: expect.objectContaining({ host: "travel.tail123.ts.net", httpPort: "3011", password: "secret", secure: true }),
		}));
		expect(renderer.root.findAllByProps({ testID: "environment-editor-host-1" })).toHaveLength(0);
	});

	it("saves a custom machine symbol from the inline appearance picker", async () => {
		const { renderer, onSave } = render();
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());
		act(() => renderer.root.findByProps({ testID: "environment-appearance-toggle-host-1" }).props.onPress());

		act(() => renderer.root.findByProps({ testID: "environment-symbol-host-1-server" }).props.onPress());
		await act(async () => renderer.root.findByProps({ testID: "environment-save-host-1" }).props.onPress());

		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({
			appearance: expect.objectContaining({ symbol: "server" }),
		}));
	});

	it("keeps an account-synced machine's route and password out of the editor", async () => {
		const onSave = vi.fn(async () => {});
		const { renderer } = render(onSave, { host: { ...host, accountUserId: "user-a" } });
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());

		expect(renderer.root.findByProps({ testID: "environment-account-managed-host-1" })).toBeTruthy();
		expect(renderer.root.findAllByProps({ testID: "environment-host-host-1" })).toHaveLength(0);
		expect(renderer.root.findAllByProps({ accessibilityLabel: "Password" })).toHaveLength(0);

		act(() => renderer.root.findByProps({ testID: "environment-name-host-1" }).props.onChangeText("Studio"));
		await act(async () => renderer.root.findByProps({ testID: "environment-save-host-1" }).props.onPress());
		expect(onSave).toHaveBeenCalledWith(expect.objectContaining({ machineName: "Studio", config: expect.objectContaining({ host: "studio.tail123.ts.net", password: "secret" }) }));
	});

	it("restores the saved symbol in the summary when editing is cancelled", () => {
		const { renderer } = render();
		const summaryAvatar = () => renderer.root.findByProps({ testID: "environment-summary-host-1" }).findByType(MachineAvatar);
		const saved = summaryAvatar().props.appearance.symbol;
		const other = saved === "cloud" ? "server" : "cloud";
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());
		act(() => renderer.root.findByProps({ testID: "environment-appearance-toggle-host-1" }).props.onPress());
		act(() => renderer.root.findByProps({ testID: `environment-symbol-host-1-${other}` }).props.onPress());
		expect(summaryAvatar().props.appearance.symbol).toBe(other);

		act(() => renderer.root.findByProps({ accessibilityLabel: "Cancel editing machine" }).props.onPress());
		expect(summaryAvatar().props.appearance.symbol).toBe(saved);
	});

	it("keeps the appearance picker symbol-only", () => {
		const { renderer } = render();
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());
		act(() => renderer.root.findByProps({ testID: "environment-appearance-toggle-host-1" }).props.onPress());

		expect(renderer.root.findAllByProps({ testID: "environment-tone-host-1-purple" })).toHaveLength(0);
	});

	it("offers the curated machine shapes with human-readable labels", () => {
		const { renderer } = render();
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());
		act(() => renderer.root.findByProps({ testID: "environment-appearance-toggle-host-1" }).props.onPress());

		expect(renderer.root.findByProps({ accessibilityLabel: "Laptop" })).toBeTruthy();
		expect(renderer.root.findByProps({ accessibilityLabel: "Mini PC" })).toBeTruthy();
		expect(renderer.root.findByProps({ accessibilityLabel: "Workstation" })).toBeTruthy();
		expect(renderer.root.findAllByProps({ accessibilityLabel: "cpu symbol" })).toHaveLength(0);
	});

	it("keeps the editor open and shows a recoverable inline save error", async () => {
		const { renderer } = render(vi.fn(async () => { throw new Error("That address is unavailable. Try again."); }));
		act(() => renderer.root.findByProps({ testID: "environment-edit-host-1" }).props.onPress());

		await act(async () => renderer.root.findByProps({ testID: "environment-save-host-1" }).props.onPress());

		expect(renderer.root.findByProps({ testID: "environment-editor-host-1" })).toBeTruthy();
		expect(renderer.root.findByProps({ testID: "environment-error-host-1" }).props.children).toBe("That address is unavailable. Try again.");
		expect(haptics.warning).toHaveBeenCalled();
	});
});
