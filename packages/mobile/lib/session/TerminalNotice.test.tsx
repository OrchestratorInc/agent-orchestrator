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

describe("the iOS terminal notice", () => {
	it("draws nothing without a notice, and leaves the snackbar host empty", async () => {
		await act(async () => { renderer = create(<TerminalNotice notice={null} onDismiss={vi.fn()} />); });
		expect(renderer!.toJSON()).toBeNull();
		await act(async () => { renderer!.update(<TerminalNoticeHost notice={{ id: 1, text: "x", tone: "error" }} onDismiss={vi.fn()} bottomOffset={0} />); });
		expect(renderer!.toJSON()).toBeNull();
	});

	it("announces a failure as an alert, and closes from a named button", async () => {
		const onDismiss = vi.fn();
		await act(async () => { renderer = create(<TerminalNotice notice={{ id: 1, text: "Message not sent.", tone: "error" }} onDismiss={onDismiss} />); });
		expect(renderer!.root.findByProps({ testID: "terminal-notice" }).props.accessibilityRole).toBe("alert");
		expect(fixtures.announce).toHaveBeenCalledWith("Message not sent.");
		// No "(tap to dismiss)": the close button says it.
		expect(renderer!.root.findByType("Text" as never).props.children).toBe("Message not sent.");
		const close = renderer!.root.findByType("Pressable" as never);
		expect(close.props).toMatchObject({ accessibilityLabel: "Dismiss", accessibilityRole: "button" });
		act(() => close.props.onPress());
		expect(onDismiss).toHaveBeenCalledOnce();
	});

	it("does not raise news as an alert", async () => {
		await act(async () => { renderer = create(<TerminalNotice notice={{ id: 1, text: "Sent.", tone: "info" }} onDismiss={vi.fn()} />); });
		expect(renderer!.root.findByProps({ testID: "terminal-notice" }).props.accessibilityRole).toBeUndefined();
	});
});

describe("the Android terminal notice", () => {
	it("hands each notice to the snackbar and clears the screen's copy", async () => {
		const onDismiss = vi.fn();
		await act(async () => {
			renderer = create(<AndroidTerminalNoticeHost notice={{ id: 3, text: "Couldn't restore the session.", tone: "error" }} onDismiss={onDismiss} bottomOffset={120} />);
		});
		const toast = () => renderer!.root.findByType("ToastHost" as never).props.toast as ToastController;
		// A failure can be closed before it times out; news cannot.
		expect(toast().current).toMatchObject({ text: "Couldn't restore the session.", duration: "long", tone: "warning", dismissible: true });
		expect(onDismiss).toHaveBeenCalledOnce();
		await act(async () => {
			renderer!.update(<AndroidTerminalNoticeHost notice={{ id: 4, text: "Sent to the terminal.", tone: "info" }} onDismiss={onDismiss} bottomOffset={0} />);
		});
		expect(toast().current).toMatchObject({ duration: "short", tone: "neutral", dismissible: false });
	});

	it("draws no inline row", async () => {
		await act(async () => { renderer = create(<AndroidTerminalNotice notice={{ id: 1, text: "x", tone: "error" }} onDismiss={vi.fn()} />); });
		expect(renderer!.toJSON()).toBeNull();
	});
});
