import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ accent: "#accent", accentTint: "#accentTint", onAccent: "#onAccent", red: "#red", textPrimary: "#primary", textSecondary: "#secondary" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./review-drawables", () => ({ reviewDrawable: (name: string) => `drawable:${name}` }));
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
import { symbols } from "./symbols";
import { touchTarget } from "./tokens";

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
	it("is a tonal Material button for the live action, leading with its symbol", async () => {
		const onPress = vi.fn();
		const root = await render(<ReviewActionButton label="Open" icon={symbols.chat} tone="filled" onPress={onPress} />);
		const button = root.findByType("FilledTonalButton" as never);
		expect(button.props).toMatchObject({ enabled: true, colors: { containerColor: "#accentTint", contentColor: "#accent" } });
		expect(button.findByType("Icon" as never).props.source).toBe("drawable:chat_bubble");
		expect(button.findByType("ComposeText" as never).props.children).toBe("Open");
		button.props.onClick();
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("outlines the others, in red for one that stops something", async () => {
		let root = await render(<ReviewActionButton label="Restore" icon={symbols.refresh} tone="outlined" onPress={() => {}} />);
		expect(root.findByType("OutlinedButton" as never).props.colors).toEqual({ contentColor: "#primary" });
		await act(async () => renderer?.unmount());
		root = await render(<ReviewActionButton label="Cancel review" icon={symbols.close} tone="destructive" onPress={() => {}} />);
		expect(root.findByType("OutlinedButton" as never).props.colors).toEqual({ contentColor: "#red" });
	});

	it("swaps the symbol for a spinner and takes no second press while busy", async () => {
		const root = await render(<ReviewActionButton label="Restore" icon={symbols.refresh} tone="outlined" busy onPress={() => {}} />);
		const button = root.findByType("OutlinedButton" as never);
		expect(button.props.enabled).toBe(false);
		expect(button.findAllByType("Icon" as never)).toHaveLength(0);
		expect(button.findAllByType("CircularProgressIndicator" as never)).toHaveLength(1);
		expect(button.findByType("ComposeText" as never).props.children).toBe("Restore");
	});
});

describe("ReviewActionButton on iOS", () => {
	it("is a small capsule system button, prominent for the live action", async () => {
		const onPress = vi.fn();
		const root = await render(<IOSReviewActionButton label="Run review" icon={{ ios: "play.fill", android: "play_arrow" }} tone="filled" onPress={onPress} />);
		const button = root.findByType("SwiftButton" as never);
		const modifiers = button.props.modifiers as Modifier[];
		expect(modifier(modifiers, "buttonStyle")).toEqual(["borderedProminent"]);
		expect(modifier(modifiers, "buttonBorderShape")).toEqual(["capsule"]);
		expect(modifier(modifiers, "controlSize")).toEqual(["small"]);
		expect(modifier(modifiers, "tint")).toEqual(["#accent"]);
		expect(modifier(modifiers, "disabled")).toEqual([false]);
		const label = root.findByType("Label" as never);
		expect(label.props).toMatchObject({ title: "Run review", systemImage: "play.fill" });
		// The accent is near white on the dark theme, so the label takes its own ink.
		expect(label.props.modifiers).toContainEqual({ modifier: "foregroundStyle", args: ["#onAccent"] });
		button.props.onPress();
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("gives a stopping action the destructive role in red", async () => {
		const root = await render(<IOSReviewActionButton label="Cancel review" icon={symbols.close} tone="destructive" onPress={() => {}} />);
		const button = root.findByType("SwiftButton" as never);
		expect(button.props.role).toBe("destructive");
		expect(modifier(button.props.modifiers, "buttonStyle")).toEqual(["bordered"]);
		expect(modifier(button.props.modifiers, "tint")).toEqual(["#red"]);
	});

	it("keeps a full touch target around the small capsule that runs the same action", async () => {
		const onPress = vi.fn();
		const root = await render(<IOSReviewActionButton label="Open" icon={symbols.chat} tone="filled" onPress={onPress} />);
		const modifiers = root.findByType("SwiftButton" as never).props.modifiers as Modifier[];
		// Last, so they wrap the capsule rather than resize it.
		expect(modifiers.slice(-3).map((entry) => entry.modifier)).toEqual(["frame", "contentShape", "onTapGesture"]);
		expect(modifier(modifiers, "frame")).toEqual([{ minHeight: touchTarget }]);
		expect(modifier(modifiers, "contentShape")).toEqual([{ type: "rectangle" }]);
		(modifier(modifiers, "onTapGesture")?.[0] as () => void)();
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("takes no tap around a disabled capsule", async () => {
		const root = await render(<IOSReviewActionButton label="Run review" icon={{ ios: "play.fill", android: "play_arrow" }} tone="filled" disabled onPress={() => {}} />);
		const modifiers = root.findByType("SwiftButton" as never).props.modifiers as Modifier[];
		expect(modifier(modifiers, "disabled")).toEqual([true]);
		expect(modifier(modifiers, "frame")).toEqual([{ minHeight: touchTarget }]);
		expect(modifier(modifiers, "onTapGesture")).toBeUndefined();
	});

	it("shows progress in place of the symbol and disables itself while busy", async () => {
		const root = await render(<IOSReviewActionButton label="Restore" icon={symbols.refresh} tone="outlined" busy onPress={() => {}} />);
		const button = root.findByType("SwiftButton" as never);
		expect(modifier(button.props.modifiers, "disabled")).toEqual([true]);
		expect(modifier(button.props.modifiers, "accessibilityValue")).toEqual(["In progress"]);
		expect(modifier(button.props.modifiers, "onTapGesture")).toBeUndefined();
		expect(root.findAllByType("ProgressView" as never)).toHaveLength(1);
		expect(root.findAllByType("Label" as never)).toHaveLength(0);
	});
});
