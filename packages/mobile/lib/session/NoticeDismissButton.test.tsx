import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: { textSecondary: "#secondary" },
}));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Platform: { OS: "ios" },
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: object) => styles },
		View: host("View"),
	};
});
vi.mock("../ThemeProvider", () => ({ useTheme: () => fixtures.theme }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: { color: "#ripple" }, pressed: { opacity: 0.7 } }) }));

import { NoticeDismissButton } from "./NoticeDismissButton";

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

describe("a notice's close button", () => {
	it("is a touch-target xmark named for what it closes", async () => {
		const onPress = vi.fn();
		await act(async () => {
			renderer = create(<NoticeDismissButton accessibilityLabel="Dismiss interface switch error" onPress={onPress} />);
		});
		const button = renderer!.root.findByType("Pressable" as never);
		expect(button.props).toMatchObject({
			accessibilityRole: "button",
			accessibilityLabel: "Dismiss interface switch error",
			accessibilityState: { disabled: false, busy: false },
			disabled: false,
			android_ripple: { color: "#ripple" },
		});
		expect(button.props.style({ pressed: false })).toContainEqual({ width: 44, height: 44, alignItems: "center", justifyContent: "center" });
		expect(button.props.style({ pressed: true })).toContainEqual({ opacity: 0.7 });
		expect(renderer!.root.findByType("SymbolView" as never).props.name.ios).toBe("xmark");
		act(() => button.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
	});

	it("shows a spinner and takes no taps while the dismissal is saved", async () => {
		await act(async () => {
			renderer = create(<NoticeDismissButton accessibilityLabel="Dismiss interface switch error" onPress={vi.fn()} busy />);
		});
		const button = renderer!.root.findByType("Pressable" as never);
		expect(button.props).toMatchObject({ disabled: true, accessibilityState: { disabled: true, busy: true } });
		expect(button.props.accessibilityLabel).toBe("Dismiss interface switch error");
		expect(renderer!.root.findAllByType("SymbolView" as never)).toHaveLength(0);
		expect(renderer!.root.findAllByType("ActivityIndicator" as never)).toHaveLength(1);
	});
});
