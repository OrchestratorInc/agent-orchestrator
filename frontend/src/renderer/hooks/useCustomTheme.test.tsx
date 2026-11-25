import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: vi.fn(), DELETE: vi.fn() },
	apiErrorMessage: (error: unknown) => String(error),
}));

import { customThemeSwitchStorageKey, setCustomThemeCatalog } from "../lib/custom-theme";
import { useUiStore } from "../stores/ui-store";
import { runAppMenuAction } from "./useAppMenuCommand";
import { useApplyCustomTheme } from "./useCustomTheme";

const sumi = {
	id: "sumi",
	name: "Sumi",
	appearance: "dark",
	colors: { canvas: "#101012" },
	variants: { light: { canvas: "#fafafa" } },
	css: "",
	updatedAt: "2026-11-19T10:00:00Z",
};

function library(themes: unknown[], request = { themeId: "", mode: "", id: "" }) {
	return { data: { themes, switch: request }, error: undefined };
}

function mount() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return renderHook(() => useApplyCustomTheme(), {
		wrapper: ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>,
	});
}

beforeEach(() => {
	getMock.mockReset();
	window.localStorage.clear();
	setCustomThemeCatalog([]);
	useUiStore.setState({ themeStyle: "orchestrate", themePreference: "dark", resolvedTheme: "dark" });
	delete document.documentElement.dataset.styleTheme;
});

afterEach(() => {
	useUiStore.setState({ themeStyle: "orchestrate" });
	delete document.documentElement.dataset.styleTheme;
	delete document.documentElement.dataset.themeCustom;
});

describe("useApplyCustomTheme", () => {
	it("applies a switch an agent asked for, once", async () => {
		getMock.mockResolvedValue(library([sumi], { themeId: "sumi", mode: "", id: "req-1" }));
		const first = mount();
		await waitFor(() => expect(useUiStore.getState().themeStyle).toBe("custom:sumi"));
		expect(window.localStorage.getItem(customThemeSwitchStorageKey)).toBe("req-1");
		first.unmount();

		// The user picks another theme; the same request must not pull them back.
		useUiStore.getState().setThemeStyle("nord");
		mount();
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(2));
		expect(useUiStore.getState().themeStyle).toBe("nord");
	});

	it("lets a one-appearance theme win over the other appearance asked for with it", async () => {
		const setThemePreference = vi.fn();
		useUiStore.setState({ setThemePreference, themePreference: "dark", resolvedTheme: "dark" });
		const night = { ...sumi, id: "night", variants: null };
		getMock.mockResolvedValue(library([night], { themeId: "night", mode: "light", id: "req-night" }));
		mount();
		await waitFor(() => expect(useUiStore.getState().themeStyle).toBe("custom:night"));
		// Light was asked for, but the theme has no light colors.
		expect(setThemePreference).not.toHaveBeenCalledWith("light");
	});

	it("applies the appearance asked for when the theme has it", async () => {
		const setThemePreference = vi.fn();
		useUiStore.setState({ setThemePreference, themePreference: "dark", resolvedTheme: "dark" });
		getMock.mockResolvedValue(library([sumi], { themeId: "sumi", mode: "light", id: "req-light" }));
		mount();
		await waitFor(() => expect(useUiStore.getState().themeStyle).toBe("custom:sumi"));
		expect(setThemePreference).toHaveBeenCalledWith("light");
	});

	it("ignores a switch to a theme that no longer exists", async () => {
		getMock.mockResolvedValue(library([], { themeId: "gone", mode: "", id: "req-2" }));
		mount();
		await waitFor(() => expect(window.localStorage.getItem(customThemeSwitchStorageKey)).toBe("req-2"));
		expect(useUiStore.getState().themeStyle).toBe("orchestrate");
	});

	it("goes back to the default theme when the one on screen was deleted", async () => {
		useUiStore.setState({ themeStyle: "custom:sumi" });
		getMock.mockResolvedValue(library([]));
		mount();
		await waitFor(() => expect(useUiStore.getState().themeStyle).toBe("orchestrate"));
	});

	it("keeps the theme on screen while the daemon cannot be read", async () => {
		useUiStore.setState({ themeStyle: "custom:sumi" });
		getMock.mockResolvedValue({ data: undefined, error: { message: "daemon down" } });
		mount();
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(useUiStore.getState().themeStyle).toBe("custom:sumi");
	});

	it("returns to the default theme from the native menu, which theme CSS cannot hide", async () => {
		useUiStore.setState({ themeStyle: "custom:sumi" });
		getMock.mockResolvedValue(library([sumi]));
		mount();
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(useUiStore.getState().themeStyle).toBe("custom:sumi");

		runAppMenuAction("use-default-theme");
		expect(useUiStore.getState().themeStyle).toBe("orchestrate");
	});

	it("leaves out a theme whose id could not have come from the daemon", async () => {
		getMock.mockResolvedValue(library([{ ...sumi, id: 'x"] *{display:none}' }, sumi]));
		mount();
		await waitFor(() => expect(getMock).toHaveBeenCalled());
		const { listCustomThemes } = await import("../lib/custom-theme");
		await waitFor(() => expect(listCustomThemes().map((theme) => theme.id)).toEqual(["sumi"]));
	});
});
