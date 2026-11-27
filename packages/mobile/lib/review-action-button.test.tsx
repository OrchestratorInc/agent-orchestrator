import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ accent: "#accent", accentTint: "#accentTint", onAccent: "#onAccent", red: "#red", textPrimary: "#primary", textSecondary: "#secondary" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("react-native", async () => {
	const React = await import("react");
	return { View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("View", props, children) };
});
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		CircularProgressIndicator: host("CircularProgressIndicator"),
		FilledTonalButton: host("FilledTonalButton"),
		Icon: host("Icon"),
		OutlinedButton: host("OutlinedButton"),
		Spacer: host("Spacer"),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	size: (width: number, height: number) => ({ size: [width, height] }),
	width: (value: number) => ({ width: value }),
}));
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Button: host("SwiftButton"), HStack: host("HStack"), Label: host("Label"), ProgressView: host("ProgressView"), Text: host("SwiftText") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		accessibilityValue: record("accessibilityValue"),
		buttonBorderShape: record("buttonBorderShape"),
		buttonStyle: record("buttonStyle"),
		contentShape: record("contentShape"),
		controlSize: record("controlSize"),
		disabled: record("disabled"),
		font: record("font"),
		foregroundStyle: record("foregroundStyle"),
		frame: record("frame"),
		onTapGesture: record("onTapGesture"),
		shapes: { rectangle: () => ({ type: "rectangle" }) },
		tint: record("tint"),
	};
});

import { ReviewActionButton } from "./review-action-button";
import { ReviewActionButton as IOSReviewActionButton } from "./review-action-button.ios";
import { touchTarget } from "./tokens";
import { symbols } from "./symbols";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

async function render(element: React.ReactElement) {
	await act(async () => { renderer = create(element); });
	return renderer!.root;
}

type Modifier = { modifier: string; args: unknown[] };
const modifier = (modifiers: Modifier[], name: string) => modifiers.find((entry) => entry.modifier === name)?.args;

describe("ReviewActionButton on Android", () => {
	it("runs its action on a press", async () => {
		const onPress = vi.fn();
		const root = await render(<ReviewActionButton label="Open" icon={symbols.chat} tone="filled" onPress={onPress} />);
		const button = root.findByType("FilledTonalButton" as never);
		expect(button.props.enabled).toBe(true);
		expect(button.findByType("ComposeText" as never).props.children).toBe("Open");
		button.props.onClick();
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("swaps the symbol for a spinner and takes no second press while busy", async () => {
		const root = await render(<ReviewActionButton label="Restore" icon={symbols.refresh} tone="outlined" busy onPress={() => {}} />);
		const button = root.findByType("OutlinedButton" as never);
		expect(button.props.enabled).toBe(false);
		expect(button.findAllByType("CircularProgressIndicator" as never)).toHaveLength(1);
	});
});

describe("ReviewActionButton on iOS", () => {
	it("runs its action from the capsule and from the full touch target around it", async () => {
		const onPress = vi.fn();
		const root = await render(<IOSReviewActionButton label="Run review" icon={{ ios: "play.fill", android: "play_arrow" }} tone="filled" onPress={onPress} />);
		const button = root.findByType("SwiftButton" as never);
		const modifiers = button.props.modifiers as Modifier[];
		expect(modifier(modifiers, "disabled")).toEqual([false]);
		expect(root.findByType("Label" as never).props.title).toBe("Run review");
		button.props.onPress();
		(modifier(modifiers, "onTapGesture")?.[0] as () => void)();
		expect(onPress).toHaveBeenCalledTimes(2);
	});

	// A Host sized to its content measured some buttons 52pt tall with the capsule
	// at the top and others 44pt with it centred, so two buttons in one row could
	// sit 17pt apart. The height is the touch target's; only the width follows the label.
	it("is always the touch target's height, its width following the label", async () => {
		const root = await render(<IOSReviewActionButton label="Send to agent" icon={symbols.send} tone="filled" onPress={() => {}} />);
		const host = root.findByType("Host" as never);
		expect(host.props.matchContents).toEqual({ horizontal: true });
		expect(host.props.style).toEqual({ height: touchTarget });
	});

	// Scrolled under the translucent header or status bar, a Host took their
	// insets and drew its capsule up to 17pt above its slot, and kept it there
	// until the next render: Send to agent sat above Reply beside it.
	it("ignores the safe area, so scrolling under the header never shifts the capsule", async () => {
		const root = await render(<IOSReviewActionButton label="Reply" icon={symbols.send} tone="outlined" onPress={() => {}} />);
		expect(root.findByType("Host" as never).props.ignoreSafeArea).toBe("all");
	});

	// A Host sized to its content is zero wide on its first frame, and the capsule
	// drawn centred on that point hung off to the left until the width arrived: a
	// "slide in from the left" each time a button mounted (Open after Resolved, a
	// row opening). The button stays hidden until its Host has a width.
	it("stays hidden until its Host has been given a width", async () => {
		const root = await render(<IOSReviewActionButton label="Send 7 open to agent" icon={symbols.send} tone="filled" onPress={() => {}} />);
		const frame = root.findByType("View" as never);
		const opacity = () => [root.findByType("View" as never).props.style].flat().reduce((value: number, style?: { opacity?: number }) => style?.opacity ?? value, 1);
		expect(opacity()).toBe(0);
		await act(async () => { frame.props.onLayout({ nativeEvent: { layout: { x: 0, y: 0, width: 0, height: 44 } } }); });
		expect(opacity()).toBe(0);
		await act(async () => { root.findByType("View" as never).props.onLayout({ nativeEvent: { layout: { x: 0, y: 0, width: 173, height: 44 } } }); });
		expect(opacity()).toBe(1);
	});

	it("gives a stopping action the destructive role", async () => {
		const root = await render(<IOSReviewActionButton label="Cancel review" icon={symbols.close} tone="destructive" onPress={() => {}} />);
		expect(root.findByType("SwiftButton" as never).props.role).toBe("destructive");
	});

	it("takes no tap around a disabled capsule", async () => {
		const root = await render(<IOSReviewActionButton label="Run review" icon={{ ios: "play.fill", android: "play_arrow" }} tone="filled" disabled onPress={() => {}} />);
		const modifiers = root.findByType("SwiftButton" as never).props.modifiers as Modifier[];
		expect(modifier(modifiers, "disabled")).toEqual([true]);
		expect(modifier(modifiers, "onTapGesture")).toBeUndefined();
	});

	it("shows progress and disables itself while busy", async () => {
		const root = await render(<IOSReviewActionButton label="Restore" icon={symbols.refresh} tone="outlined" busy onPress={() => {}} />);
		const button = root.findByType("SwiftButton" as never);
		expect(modifier(button.props.modifiers, "disabled")).toEqual([true]);
		expect(modifier(button.props.modifiers, "accessibilityValue")).toEqual(["In progress"]);
		expect(modifier(button.props.modifiers, "onTapGesture")).toBeUndefined();
		expect(root.findAllByType("ProgressView" as never)).toHaveLength(1);
	});
});
