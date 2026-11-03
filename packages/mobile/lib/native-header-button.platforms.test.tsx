import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ textSecondary: "#secondary", red: "#red" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./glass", () => ({ glassCircle: () => ({ modifier: "glassCircle" }) }));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("Host", props, children) };
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		IconButton: host("IconButton"),
		Icon: host("Icon"),
		Badge: host("Badge"),
		BadgedBox: Object.assign(host("BadgedBox"), { Badge: host("BadgeSlot") }),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({ testID: (tag: string) => ({ testID: tag }) }));
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Circle: host("Circle"), Image: host("Image"), ZStack: host("ZStack") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		accessibilityAddTraits: record("accessibilityAddTraits"),
		accessibilityElement: record("accessibilityElement"),
		accessibilityIdentifier: record("accessibilityIdentifier"),
		accessibilityLabel: record("accessibilityLabel"),
		contentShape: record("contentShape"),
		foregroundStyle: record("foregroundStyle"),
		frame: record("frame"),
		offset: record("offset"),
		onTapGesture: record("onTapGesture"),
		shapes: { circle: () => ({ shape: "circle" }) },
	};
});

import { NativeHeaderButton as AndroidHeaderButton } from "./native-header-button.android";
import { NativeHeaderButton as IOSHeaderButton } from "./native-header-button.ios";
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

describe("NativeHeaderButton on Android", () => {
	it("presses through Material's icon button, described for TalkBack", async () => {
		const onPress = vi.fn();
		const root = await render(<AndroidHeaderButton icon="more" label="Conversation actions" onPress={onPress} />);

		const button = root.findByType("IconButton" as never);
		expect(button.findByType("Icon" as never).props.contentDescription).toBe("Conversation actions");
		expect(root.findAllByType("BadgedBox" as never)).toHaveLength(0);
		await act(async () => button.props.onClick());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("shows unread as a badge on the glyph", async () => {
		const root = await render(<AndroidHeaderButton icon="bell" label="Notifications, 3 unread" badge={3} onPress={() => {}} />);
		expect(root.findByType("BadgedBox" as never).findAllByType("Badge" as never)).toHaveLength(1);
	});
});

describe("NativeHeaderButton on iOS", () => {
	// The unread dot is the red circle; the clear one is what sizes the element.
	const dots = (root: ReactTestRenderer["root"]) => root.findAllByType("Circle" as never)
		.filter((circle) => (circle.props.modifiers as Modifier[]).some((entry) => entry.modifier === "foregroundStyle" && entry.args[0] === "#red"));


	it("is one VoiceOver button the size of a touch target, with the unread dot inside it", async () => {
		const root = await render(<IOSHeaderButton icon="bell" label="Notifications, 3 unread" badge={3} onPress={() => {}} />);

		const modifiers = root.findAllByType("ZStack" as never)[0].props.modifiers as Modifier[];
		expect(modifiers.find((entry) => entry.modifier === "accessibilityElement")?.args).toEqual(["ignore"]);
		expect(modifiers.find((entry) => entry.modifier === "accessibilityLabel")?.args).toEqual(["Notifications, 3 unread"]);
		expect(modifiers.find((entry) => entry.modifier === "accessibilityAddTraits")).toBeDefined();
		expect(modifiers.find((entry) => entry.modifier === "frame")?.args).toEqual([{ width: touchTarget, height: touchTarget }]);
		expect(dots(root.findAllByType("ZStack" as never)[1])).toHaveLength(1);
	});

	// VoiceOver sizes the element from its content, not from `frame`.
	it("gives VoiceOver the whole circle, not the glyph", async () => {
		const root = await render(<IOSHeaderButton icon="back" label="Back to Settings" onPress={() => {}} />);
		const stack = root.findAllByType("ZStack" as never)[0];
		expect((stack.props.modifiers as Modifier[]).find((entry) => entry.modifier === "contentShape")?.args).toEqual([{ shape: "circle" }, ["interaction", "accessibility"]]);
		const fill = stack.findAllByType("Circle" as never).find((circle) => (circle.props.modifiers as Modifier[]).some((entry) => entry.modifier === "frame" && (entry.args[0] as { width: number }).width === touchTarget));
		expect(fill).toBeDefined();
	});

	it("draws no dot without unread", async () => {
		const root = await render(<IOSHeaderButton icon="back" label="Back to Settings" onPress={() => {}} />);
		expect(dots(root)).toHaveLength(0);
	});
});
