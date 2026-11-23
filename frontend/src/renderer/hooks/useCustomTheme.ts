/**
 * Custom themes: theme files the user imports in Settings or their agents make
 * (`ao theme`). The daemon owns them so any session's agent can make one and
 * every window lists the same themes.
 */

import { useEffect } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import {
	customThemeCssChanged,
	customThemeHasAppearance,
	customThemeStyle,
	isCustomThemeStyle,
	rememberCustomThemeStyle,
	setCustomThemeCatalog,
	showCustomThemeStyle,
	takeCustomThemeSwitch,
	upsertCustomTheme,
	type CustomTheme,
	type CustomThemeSwitchRequest,
} from "../lib/custom-theme";
import { runThemeTransition } from "../lib/theme";
import { useAppMenuAction, useAppMenuAvailable } from "./useAppMenuCommand";
import { DEFAULT_THEME_STYLE, isThemeStyle } from "../lib/theme-palettes";
import { useUiStore } from "../stores/ui-store";

export const customThemeQueryKey = ["custom-themes"] as const;

const customThemeReadTimeoutMs = 10_000;

export type CustomThemes = { themes: CustomTheme[]; switch: CustomThemeSwitchRequest };

export function useCustomThemes() {
	return useQuery({
		queryKey: customThemeQueryKey,
		// A change arrives at once as a CDC event (event-transport.ts). A daemon
		// with no project yet logs no event, so returning to the window reads
		// again. No timer: the answer carries every theme's CSS, which can be
		// megabytes of embedded images.
		refetchOnWindowFocus: true,
		retry: 5,
		queryFn: async ({ signal }): Promise<CustomThemes> => {
			const { data, error } = await apiClient.GET("/api/v1/settings/themes", {
				signal: AbortSignal.any([signal, AbortSignal.timeout(customThemeReadTimeoutMs)]),
			});
			if (error) throw error;
			return {
				themes: data?.themes ?? [],
				switch: data?.switch ?? { themeId: "", mode: "", id: "" },
			};
		},
	});
}

/** Keeps the page in step with the daemon's custom themes. Mount once, in the shell. */
export function useApplyCustomTheme(): void {
	const { data } = useCustomThemes();
	// View > Use Default Theme (macOS): theme CSS can hide Settings or the whole
	// window, and a native menu item is the one control it cannot reach.
	const customShown = useUiStore((state) => isCustomThemeStyle(state.themeStyle));
	useAppMenuAvailable("use-default-theme", customShown);
	useAppMenuAction("use-default-theme", () => useUiStore.getState().setThemeStyle(DEFAULT_THEME_STYLE));
	useEffect(() => {
		// Until the daemon answers, the copy saved at startup stays on screen.
		if (!data) return;
		setCustomThemeCatalog(data.themes);
		const ui = useUiStore.getState();
		// An agent asked for a theme or light/dark (`ao theme use|mode`, or
		// `set --use`): switch once, through the same setters as Settings.
		const request = takeCustomThemeSwitch(data.switch);
		// A theme with colors for one appearance shows in that one (setThemeStyle
		// switches to it). Asked for together with the other appearance, the
		// theme wins; applying both would leave the result to timing.
		const asked = request?.themeId ? data.themes.find((theme) => theme.id === request.themeId) : undefined;
		const mode = request?.mode;
		const themeLacksMode = asked !== undefined && (mode === "light" || mode === "dark") && !customThemeHasAppearance(asked, mode);
		if (mode && !themeLacksMode) ui.setThemePreference(mode);
		if (request?.themeId) {
			if (isThemeStyle(request.themeId)) ui.setThemeStyle(request.themeId);
			else if (data.themes.some((theme) => theme.id === request.themeId)) ui.setThemeStyle(customThemeStyle(request.themeId));
		}
		const shown = useUiStore.getState().themeStyle;
		if (!isCustomThemeStyle(shown)) return;
		if (!data.themes.some((theme) => customThemeStyle(theme.id) === shown)) {
			// The theme on screen was deleted.
			ui.setThemeStyle(DEFAULT_THEME_STYLE);
			return;
		}
		// An agent edited the theme on screen: swap under the same instant cut
		// as a theme switch, so nothing tweens between colors.
		if (customThemeCssChanged(shown)) runThemeTransition(() => showCustomThemeStyle(shown));
		rememberCustomThemeStyle(shown);
	}, [data]);
}

/**
 * Imports a theme file (the parsed JSON). With `keepExisting` the daemon
 * refuses (CUSTOM_THEME_EXISTS) rather than replace a theme with the same id.
 * `id` stores it under that id whatever the file says.
 */
export function useImportCustomTheme() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ file, keepExisting, id }: { file: Record<string, unknown>; keepExisting: boolean; id?: string }) => {
			const { data, error } = await apiClient.POST("/api/v1/settings/themes", { body: { file, keepExisting, ...(id ? { id } : {}) } });
			if (error) throw error;
			if (!data) throw new Error("The daemon returned no theme.");
			return data;
		},
		onSuccess: (data) => {
			// Only the imported theme is taken from this answer, so the caller can
			// switch to it at once. The list is read again: an answer that arrives
			// late must not put an older list over a newer one.
			upsertCustomTheme(data.theme);
			queryClient.setQueryData<CustomThemes>(customThemeQueryKey, (current) =>
				current
					? {
							...current,
							themes: current.themes.some((theme) => theme.id === data.theme.id)
								? current.themes.map((theme) => (theme.id === data.theme.id ? data.theme : theme))
								: [...current.themes, data.theme],
						}
					: current,
			);
			void queryClient.invalidateQueries({ queryKey: customThemeQueryKey });
		},
	});
}

export function useDeleteCustomTheme() {
	const queryClient = useQueryClient();
	const mutation = useMutation({
		mutationFn: async (id: string) => {
			const { data, error } = await apiClient.DELETE("/api/v1/settings/themes/{themeId}", { params: { path: { themeId: id } } });
			if (error) throw error;
			return data;
		},
		// Read the list again instead of trusting this answer's copy of it: a
		// late answer could show a theme as deleted that still exists, and the
		// page would then fall back to the default theme.
		onSuccess: () => queryClient.invalidateQueries({ queryKey: customThemeQueryKey }),
	});
	return {
		remove: (id: string) => mutation.mutate(id),
		removing: mutation.isPending,
		error: mutation.error ? apiErrorMessage(mutation.error) : undefined,
	};
}
