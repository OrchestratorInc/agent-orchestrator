import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" }, tap: vi.fn() }));

vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		ActivityIndicator: host("ActivityIndicator"),
		Platform: fixtures.platform,
		Pressable: host("Pressable"),
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 0.5 },
		Text: host("Text"),
		View: host("View"),
	};
});
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (factory: (theme: unknown) => unknown) => factory(darkTheme) };
});

import { InlineBanner } from "./InlineBanner";
import { darkTheme } from "./theme";
import { touchTarget, type } from "./tokens";

let renderer: ReactTestRenderer | undefined;

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	fixtures.tap.mockReset();
});

function render(element: React.ReactElement): ReactTestRenderer["root"] {
	act(() => {
		renderer = create(element);
	});
	return renderer!.root;
}

function flat(style: unknown): Record<string, unknown> {
	const resolved = typeof style === "function" ? style({ pressed: false }) : style;
	return Object.assign({}, ...[resolved].flat(Infinity).filter(Boolean));
}

describe("InlineBanner", () => {
	it("gives its actions and close button full touch targets with button roles", () => {
		const onPress = vi.fn();
		const onSecondary = vi.fn();
		const onDismiss = vi.fn();
		const root = render(<InlineBanner tone="danger" icon="warning" title="The agent is stopped" body="It exited." action="Resume" secondary="Shell" onPress={onPress} onSecondary={onSecondary} onDismiss={onDismiss} />);
		const buttons = root.findAllByType("Pressable" as never);
		expect(buttons).toHaveLength(3);
		for (const button of buttons) {
			expect(button.props.accessibilityRole).toBe("button");
			const style = flat(button.props.style);
			expect(style.minHeight ?? style.height).toBe(touchTarget);
			expect(style.minWidth ?? style.width).toBe(touchTarget);
			expect(button.props.hitSlop).toBeUndefined();
		}
		expect(buttons[2].props.accessibilityLabel).toBe("Close: The agent is stopped");
		act(() => buttons[1].props.onPress());
		expect(onPress).toHaveBeenCalledTimes(1);
		act(() => buttons[2].props.onPress());
		expect(onDismiss).toHaveBeenCalledTimes(1);
		// Closing is not worth a haptic; the action is.
		expect(fixtures.tap).toHaveBeenCalledTimes(1);
	});

	it("reads at footnote size, never caption", () => {
		const root = render(<InlineBanner tone="warning" icon="warning" title="80% of quota used" body="Resets in 2h." action="Details" onPress={() => {}} />);
		for (const text of root.findAllByType("Text" as never)) {
			expect(flat(text.props.style).fontSize).toBe(type.footnote.fontSize);
		}
	});

	it("shows the system spinner for a state still in progress", () => {
		const root = render(<InlineBanner tone="warning" icon="busy" title="Reconnecting to the agent…" />);
		const spinner = root.findByType("ActivityIndicator" as never);
		expect(spinner.props.color).toBe(darkTheme.amber);
		expect(root.findAllByType("SymbolView" as never)).toHaveLength(0);
	});

	it("leaves a busy action in place but inert", () => {
		const root = render(<InlineBanner tone="danger" icon="warning" title="Session failed to start" action="Retrying…" />);
		const action = root.findByType("Pressable" as never);
		expect(action.props.disabled).toBe(true);
		expect(action.props.accessibilityState).toEqual({ disabled: true });
	});

	it("draws the Android ripple instead of fading", () => {
		fixtures.platform.OS = "android";
		try {
			const root = render(<InlineBanner tone="muted" icon="warning" title="1 turn was rolled back" onDismiss={() => {}} />);
			const close = root.findByType("Pressable" as never);
			expect(close.props.android_ripple).toMatchObject({ borderless: true });
			expect(flat(close.props.style({ pressed: true })).opacity).toBeUndefined();
		} finally {
			fixtures.platform.OS = "ios";
		}
	});
});
