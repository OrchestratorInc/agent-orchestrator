import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" }, router: { back: vi.fn() }, backOr: vi.fn() }));

vi.mock("react-native", () => ({ Platform: fixtures.platform }));
vi.mock("./backNavigation", () => ({ backOr: fixtures.backOr }));
vi.mock("expo-router", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Stack: { Toolbar: Object.assign(host("StackToolbar"), { Button: host("StackToolbarButton") }) }, useRouter: () => fixtures.router };
});

import { StackRootBack } from "./StackRootBack";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.backOr.mockClear();
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

describe("StackRootBack", () => {
	// UIKit draws no back button for the first page of a navigation controller,
	// so a stack pushed over the board adds the chevron where the system's sits.
	it("puts a back chevron at the bar's leading end on iOS, popping the page", () => {
		fixtures.platform.OS = "ios";
		act(() => { renderer = create(<StackRootBack />); });
		expect(renderer!.root.findByType("StackToolbar" as never).props.placement).toBe("left");
		const back = renderer!.root.findByType("StackToolbarButton" as never);
		expect(back.props).toMatchObject({ icon: "chevron.backward", accessibilityLabel: "Back" });
		back.props.onPress();
		expect(fixtures.backOr).toHaveBeenCalledWith(fixtures.router);
	});

	// Android's bar already draws one from the parent stack.
	it("adds nothing on Android", () => {
		fixtures.platform.OS = "android";
		act(() => { renderer = create(<StackRootBack />); });
		expect(renderer!.toJSON()).toBeNull();
	});
});
