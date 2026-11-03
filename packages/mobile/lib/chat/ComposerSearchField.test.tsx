import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("expo-router", () => ({ Stack: { SearchBar: "StackSearchBar" } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({ highlight: "highlight", textPrimary: "text", textTertiary: "hint", accent: "accent" }) }));

import { ComposerSearchField } from "./ComposerSearchField";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
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

function render(initialQuery: string) {
	const onQuery = vi.fn();
	act(() => { renderer = create(<ComposerSearchField initialQuery={initialQuery} placeholder="Search skills" onQuery={onQuery} />); });
	return { onQuery, bar: () => renderer!.root.findByType("StackSearchBar" as never) };
}

describe("iOS composer search", () => {
	it("searches in the navigation bar, filtering in place", () => {
		const { bar, onQuery } = render("");
		expect(bar().props.placeholder).toBe("Search skills");
		act(() => bar().props.onChangeText({ nativeEvent: { text: "rev" } }));
		expect(onQuery).toHaveBeenLastCalledWith("rev");
		act(() => bar().props.onCancelButtonPress());
		expect(onQuery).toHaveBeenLastCalledWith("");
	});

	// The header hands the bar's commands over on every render of the bar; only
	// the first may write, or the user's typing would be replaced.
	it("opens on the composer's query, once", () => {
		const { bar } = render("rev");
		const commands = { setText: vi.fn() };
		bar().props.ref.current = commands;
		bar().props.ref.current = null;
		bar().props.ref.current = commands;
		expect(commands.setText).toHaveBeenCalledExactlyOnceWith("rev");
		expect(bar().props.ref.current).toBe(commands);
	});
});
