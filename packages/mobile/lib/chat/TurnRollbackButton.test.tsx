import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type AlertButton = { text: string; style?: string; onPress?(): void };

const { alert } = vi.hoisted(() => ({ alert: vi.fn<(title: string, message?: string, buttons?: AlertButton[], options?: object) => void>() }));

vi.mock("react-native", () => ({
	Alert: { alert },
	ActivityIndicator: "ActivityIndicator",
	Platform: { OS: "ios" },
	Pressable: "Pressable",
	StyleSheet: { create: (styles: object) => styles },
	View: "View",
}));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({ textTertiary: "#tertiary", textSecondary: "#secondary" }) }));
vi.mock("../pressFeedback", () => ({ usePressFeedback: () => ({ android_ripple: undefined, pressed: { opacity: 0.7 } }) }));
vi.mock("../haptics", () => ({ haptics: { warning: vi.fn() } }));

import { haptics } from "../haptics";
import { touchTarget } from "../tokens";
import { TurnRollbackButton } from "./TurnRollbackButton";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	alert.mockClear();
	vi.mocked(haptics.warning).mockClear();
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

function render(onRollback: (turnId: string) => Promise<number>) {
	act(() => { renderer = create(<TurnRollbackButton turnId="turn-7" onRollback={onRollback} />); });
	return () => renderer!.root.findByProps({ accessibilityLabel: "Roll back to before this turn" });
}

/** Presses the control and returns the confirmation's buttons. */
function press(button: ReturnType<typeof render>): AlertButton[] {
	act(() => button().props.onPress());
	return alert.mock.lastCall?.[2] ?? [];
}

const choose = (buttons: AlertButton[], text: string) => buttons.find((button) => button.text === text)!;
const flatten = (style: unknown): Record<string, unknown> => Array.isArray(style) ? Object.assign({}, ...style.map(flatten)) : (style as Record<string, unknown>) || {};

describe("TurnRollbackButton", () => {
	it("is a labelled button the size of a touch target, in the system's undo glyph", () => {
		const button = render(vi.fn(async () => 0));
		expect(button().props.accessibilityRole).toBe("button");
		expect(button().props.accessibilityState).toEqual({ disabled: false, busy: false });
		expect(flatten(button().props.style({ pressed: false }))).toMatchObject({ width: touchTarget, height: touchTarget });
		expect(button().findByType("SymbolView" as never).props.name).toEqual({ ios: "arrow.uturn.backward", android: "undo" });
	});

	// Forgetting a turn cannot be undone, so it asks through the system's own
	// destructive confirmation first.
	it("asks with a native destructive alert and does nothing on Cancel", () => {
		const onRollback = vi.fn(async () => 0);
		const buttons = press(render(onRollback));
		expect(haptics.warning).toHaveBeenCalledOnce();
		expect(alert).toHaveBeenCalledOnce();
		const [title, message, , options] = alert.mock.lastCall!;
		expect(title).toBe("Make the agent forget this turn and everything after it?");
		expect(message).toBe("Files stay changed. Only conversation memory is rolled back, and this cannot be undone.");
		expect(options).toEqual({ cancelable: true });
		expect(buttons.map(({ text, style }) => ({ text, style }))).toEqual([{ text: "Cancel", style: "cancel" }, { text: "Roll back", style: "destructive" }]);
		expect(choose(buttons, "Cancel").onPress).toBeUndefined();
		expect(onRollback).not.toHaveBeenCalled();
	});

	it("rolls back on confirm, busy until the daemon answers", async () => {
		let answer!: (removed: number) => void;
		const onRollback = vi.fn(() => new Promise<number>((resolve) => { answer = resolve; }));
		const button = render(onRollback);
		act(() => choose(press(button), "Roll back").onPress!());
		expect(onRollback).toHaveBeenCalledExactlyOnceWith("turn-7");
		expect(button().props.disabled).toBe(true);
		expect(button().props.accessibilityState).toEqual({ disabled: true, busy: true });
		expect(button().findAllByType("ActivityIndicator" as never)).toHaveLength(1);
		expect(button().findAllByType("SymbolView" as never)).toHaveLength(0);
		await act(async () => answer(3));
		expect(button().props.accessibilityState).toEqual({ disabled: false, busy: false });
		expect(button().findAllByType("SymbolView" as never)).toHaveLength(1);
		expect(alert).toHaveBeenCalledOnce();
	});

	it("says why when the rollback fails, and lets you try again", async () => {
		const onRollback = vi.fn(async () => { throw new Error("The provider has no record of this turn."); });
		const button = render(onRollback);
		await act(async () => choose(press(button), "Roll back").onPress!());
		expect(alert).toHaveBeenLastCalledWith("Couldn't roll back", "The provider has no record of this turn.");
		expect(button().props.accessibilityState).toEqual({ disabled: false, busy: false });
		press(button);
		expect(alert).toHaveBeenCalledTimes(3);
	});
});
