import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { readFileSync } from "node:fs";
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
		expect(bar().props).toMatchObject({ placeholder: "Search skills", placement: "stacked", hideWhenScrolling: false, obscureBackground: false, autoCapitalize: "none", tintColor: "highlight" });
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

	it("leaves the bar alone when there is no query", () => {
		const { bar } = render("");
		const commands = { setText: vi.fn() };
		bar().props.ref.current = commands;
		expect(commands.setText).not.toHaveBeenCalled();
	});
});

describe("Android composer search", () => {
	// Compose's DockedSearchBar cannot open on a query, so Android builds
	// Material's search field from a pill-shaped TextField it can seed.
	const android = readFileSync(new URL("./ComposerSearchField.android.tsx", import.meta.url), "utf8");

	it("is Material's search field, seeded with the composer's query", () => {
		expect(android).toContain("const text = useNativeState(initialQuery);");
		expect(android).toContain("shape={Shape.Pill({})}");
		expect(android).toContain("<TextField.LeadingIcon><Icon source={SEARCH} /></TextField.LeadingIcon>");
		expect(android).toContain('<Icon source={materialDrawable("close")} contentDescription="Clear search" />');
	});
});
