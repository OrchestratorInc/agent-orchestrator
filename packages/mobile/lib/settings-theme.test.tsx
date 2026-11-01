import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { THEMES } from "../../shared/theme/palettes";
import { themeFor } from "./theme";

const fixtures = vi.hoisted(() => ({ setStyle: vi.fn(), select: vi.fn(), style: "nord" }));

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles }, View: "View" }));
vi.mock("./SettingsForm", () => ({ SettingsForm: "SettingsForm", SettingsChoiceSection: "SettingsChoiceSection" }));
vi.mock("./ThemePaletteSwatch", () => ({ ThemePaletteSwatch: "ThemePaletteSwatch" }));
vi.mock("./haptics", () => ({ haptics: { select: fixtures.select } }));
vi.mock("./RouteErrorBoundary", () => ({ RouteErrorBoundary: () => null }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ bgBase: "#base" }),
	useThemeState: () => ({ scheme: "dark", style: fixtures.style, setStyle: fixtures.setStyle }),
}));

import ThemeScreen from "../app/settings/theme";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixtures.setStyle.mockClear();
	fixtures.select.mockClear();
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

function choices() {
	act(() => { renderer = create(<ThemeScreen />); });
	return renderer!.root.findByType("SettingsForm" as never).findByType("SettingsChoiceSection" as never);
}

describe("the theme picker", () => {
	// An inline picker on iOS, radio buttons on Android (lib/SettingsForm).
	it("offers every desktop theme as one native choice, the current one chosen", () => {
		const section = choices();
		expect(section.props.label).toBe("Theme");
		expect(section.props.selectedValue).toBe("nord");
		expect(section.props.testID).toBe("settings-theme");
		expect(section.props.options.map((option: { value: string; label: string }) => [option.value, option.label])).toEqual(THEMES.map((theme) => [theme.id, theme.label]));
	});

	it("previews each theme with its swatch in the scheme showing now", () => {
		const [first] = choices().props.options as { value: string; accessory: React.ReactElement<{ theme: unknown }> }[];
		expect(first.accessory.type).toBe("ThemePaletteSwatch");
		expect(first.accessory.props.theme).toEqual(themeFor("dark", THEMES[0].id));
	});

	it("applies a new theme at once, and ignores choosing the current one", () => {
		const section = choices();
		act(() => section.props.onValueChange("dracula"));
		expect(fixtures.setStyle).toHaveBeenCalledWith("dracula");
		expect(fixtures.select).toHaveBeenCalledOnce();

		act(() => section.props.onValueChange("nord"));
		expect(fixtures.setStyle).toHaveBeenCalledOnce();
	});

	// The card used to be the scroll viewport so its corners stayed round; the
	// native form scrolls and clips itself.
	it("lets the native form scroll the list", () => {
		const source = readFileSync(fileURLToPath(new URL("../app/settings/theme.tsx", import.meta.url)), "utf8");
		for (const workaround of ["ScrollView", "Pressable", "overflow", "borderRadius", "hairlineWidth", "useSafeAreaInsets"]) expect(source).not.toContain(workaround);
	});
});
