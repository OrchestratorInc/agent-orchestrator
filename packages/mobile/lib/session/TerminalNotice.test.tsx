import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: { red: "#red", textPrimary: "#primary", textSecondary: "#secondary", bgElevated: "#elevated", tintRed: "#tintRed", borderSubtle: "#subtle" },
	announce: vi.fn(),
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		AccessibilityInfo: { announceForAccessibility: fixtures.announce },
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: object) => styles },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("../ThemeProvider", () => ({
	useTheme: () => fixtures.theme,
	useThemedStyles: (factory: (t: typeof fixtures.theme) => unknown) => factory(fixtures.theme),
}));
vi.mock("../divider", () => ({ rowDividerWidth: 1 }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: { opacity: 0.7 } }) }));
vi.mock("../Toast", async () => {
	const React = await import("react");
	return { ToastHost: (props: object) => React.createElement("ToastHost", props) };
});

import { TerminalNotice, TerminalNoticeHost } from "./TerminalNotice";
import { TerminalNotice as AndroidTerminalNotice, TerminalNoticeHost as AndroidTerminalNoticeHost } from "./TerminalNotice.android";
import type { ToastController } from "../Toast.types";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.announce.mockReset();
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

const symbols = () => renderer!.root.findAllByType("SymbolView" as never).map((node) => node.props.name.ios);

describe("the iOS terminal notice", () => {
	it("draws nothing without a notice", async () => {
		await act(async () => { renderer = create(<TerminalNotice notice={null} onDismiss={vi.fn()} />); });
		expect(renderer!.toJSON()).toBeNull();
	});

	it("marks a failure in red with an error symbol and announces it as an alert", async () => {
		await act(async () => { renderer = create(<TerminalNotice notice={{ id: 1, text: "Message not sent.", tone: "error" }} onDismiss={vi.fn()} />); });
		const row = renderer!.root.findByProps({ testID: "terminal-notice" });
		expect(row.props.accessibilityRole).toBe("alert");
		expect(row.props.style).toContainEqual({ backgroundColor: "#tintRed" });
		expect(symbols()).toEqual(["exclamationmark.circle", "xmark"]);
		expect(renderer!.root.findAllByType("SymbolView" as never)[0].props.tintColor).toBe("#red");
		expect(fixtures.announce).toHaveBeenCalledWith("Message not sent.");
		// No "(tap to dismiss)": the close button says it.
		expect(renderer!.root.findByType("Text" as never).props.children).toBe("Message not sent.");
	});

	it("tells news apart from a failure", async () => {
		await act(async () => { renderer = create(<TerminalNotice notice={{ id: 1, text: "Sending composer text straight to the terminal.", tone: "info" }} onDismiss={vi.fn()} />); });
		const row = renderer!.root.findByProps({ testID: "terminal-notice" });
		expect(row.props.accessibilityRole).toBeUndefined();
		expect(row.props.style).not.toContainEqual({ backgroundColor: "#tintRed" });
		expect(symbols()[0]).toBe("info.circle");
	});

	it("closes from a touch-target button with a name", async () => {
		const onDismiss = vi.fn();
		await act(async () => { renderer = create(<TerminalNotice notice={{ id: 1, text: "x", tone: "error" }} onDismiss={onDismiss} />); });
		const close = renderer!.root.findByProps({ accessibilityLabel: "Dismiss" });
		expect(close.props.accessibilityRole).toBe("button");
		act(() => close.props.onPress());
		expect(onDismiss).toHaveBeenCalledOnce();
	});

	it("leaves the snackbar host empty", async () => {
		await act(async () => { renderer = create(<TerminalNoticeHost notice={{ id: 1, text: "x", tone: "error" }} onDismiss={vi.fn()} bottomOffset={0} />); });
		expect(renderer!.toJSON()).toBeNull();
	});
});

describe("the Android terminal notice", () => {
	it("hands each notice to the snackbar and clears the screen's copy", async () => {
		const onDismiss = vi.fn();
		await act(async () => {
			renderer = create(<AndroidTerminalNoticeHost notice={{ id: 3, text: "Couldn't restore the session.", tone: "error" }} onDismiss={onDismiss} bottomOffset={120} />);
		});
		const host = renderer!.root.findByType("ToastHost" as never);
		const toast = host.props.toast as ToastController;
		expect(host.props.bottomOffset).toBe(120);
		expect(toast.current).toMatchObject({ text: "Couldn't restore the session.", duration: "long", tone: "warning" });
		expect(onDismiss).toHaveBeenCalledOnce();
	});

	it("keeps news brief", async () => {
		await act(async () => {
			renderer = create(<AndroidTerminalNoticeHost notice={{ id: 4, text: "Sent to the terminal.", tone: "info" }} onDismiss={vi.fn()} bottomOffset={0} />);
		});
		const toast = renderer!.root.findByType("ToastHost" as never).props.toast as ToastController;
		expect(toast.current).toMatchObject({ duration: "short", tone: "neutral" });
	});

	it("draws no inline row", async () => {
		await act(async () => { renderer = create(<AndroidTerminalNotice notice={{ id: 1, text: "x", tone: "error" }} onDismiss={vi.fn()} />); });
		expect(renderer!.toJSON()).toBeNull();
	});
});
