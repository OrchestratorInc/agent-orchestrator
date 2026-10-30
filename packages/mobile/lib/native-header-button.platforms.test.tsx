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
	return { Circle: host("Circle"), Group: host("Group"), Image: host("Image"), ZStack: host("ZStack") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const record = (name: string) => (...args: unknown[]) => ({ modifier: name, args });
	return {
		accessibilityAddTraits: record("accessibilityAddTraits"),
		accessibilityElement: record("accessibilityElement"),
		accessibilityIdentifier: record("accessibilityIdentifier"),
		accessibilityLabel: record("accessibilityLabel"),
		foregroundStyle: record("foregroundStyle"),
		frame: record("frame"),
		offset: record("offset"),
		onTapGesture: record("onTapGesture"),
	};
});

import { NativeHeaderButton as AndroidHeaderButton } from "./native-header-button.android";
import { GLASS_CIRCLE_SIZE, NativeHeaderButton as IOSHeaderButton } from "./native-header-button.ios";
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
const modifierNames = (modifiers: Modifier[]) => modifiers.map((entry) => entry.modifier);

describe("NativeHeaderButton on Android", () => {
	it("is Material's icon button around a bundled Material Symbol", async () => {
		const onPress = vi.fn();
		const root = await render(<AndroidHeaderButton icon="more" label="Conversation actions" onPress={onPress} />);

		const button = root.findByType("IconButton" as never);
		expect(button.props.modifiers).toEqual([{ testID: "header-more" }]);
		const icon = button.findByType("Icon" as never);
		expect(icon.props.source).toBe("drawable:more_vert");
		expect(icon.props.contentDescription).toBe("Conversation actions");
		expect(icon.props.size).toBe(24);
		expect(root.findAllByType("BadgedBox" as never)).toHaveLength(0);

		await act(async () => button.props.onClick());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("shows unread as Material's badge on the glyph", async () => {
		const root = await render(<AndroidHeaderButton icon="bell" label="Notifications, 3 unread" badge={3} onPress={() => {}} />);

		const badged = root.findByType("BadgedBox" as never);
		expect(badged.findByType("Icon" as never).props.source).toBe("drawable:notifications");
		expect(badged.findByType("BadgeSlot" as never).findByType("Badge" as never).props.containerColor).toBe("#red");
	});
});

describe("NativeHeaderButton on iOS", () => {
	it("keeps the glass circle with the SF Symbol and the button trait", async () => {
		const root = await render(<IOSHeaderButton icon="back" label="Back to Settings" onPress={() => {}} />);

		const group = root.findByType("Group" as never);
		expect(modifierNames(group.props.modifiers)).toEqual(["frame", "glassCircle", "onTapGesture", "accessibilityAddTraits", "accessibilityLabel", "accessibilityIdentifier"]);
		// The whole circle takes the tap, so it is the touch target.
		expect(GLASS_CIRCLE_SIZE).toBe(touchTarget);
		expect((group.props.modifiers as Modifier[])[0].args).toEqual([{ width: touchTarget, height: touchTarget }]);
		expect(root.findByType("Host" as never).props.style).toEqual({ width: touchTarget, height: touchTarget });
		expect(root.findByType("Image" as never).props.systemName).toBe("chevron.backward");
		expect(root.findAllByType("Circle" as never)).toHaveLength(0);
	});

	it("draws the unread dot inside the circle and stays one VoiceOver element", async () => {
		const root = await render(<IOSHeaderButton icon="bell" label="Notifications, 3 unread" badge={3} onPress={() => {}} />);

		const group = root.findByType("Group" as never);
		const modifiers = group.props.modifiers as Modifier[];
		expect(modifiers.find((entry) => entry.modifier === "accessibilityElement")?.args).toEqual(["ignore"]);
		expect(modifiers.find((entry) => entry.modifier === "accessibilityLabel")?.args).toEqual(["Notifications, 3 unread"]);
		const stack = group.findByType("ZStack" as never);
		expect(stack.props.alignment).toBe("topTrailing");
		const dot = stack.findByType("Circle" as never);
		expect((dot.props.modifiers as Modifier[]).find((entry) => entry.modifier === "foregroundStyle")?.args).toEqual(["#red"]);
	});
});
