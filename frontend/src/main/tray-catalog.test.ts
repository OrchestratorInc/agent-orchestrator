import { describe, expect, it } from "vitest";
import { catalogFor } from "../renderer/i18n/all-messages";
import { APP_LOCALES } from "../shared/ui-locale";
import { TRAY_MESSAGE_KEYS, trayCatalogs } from "./tray-catalog.generated";

describe("tray catalog", () => {
	it.each(APP_LOCALES)("matches the renderer catalog for %s (run `node scripts/sync-tray-catalog.mjs` if this fails)", (locale) => {
		const renderer = catalogFor(locale) as Record<string, string>;
		for (const key of TRAY_MESSAGE_KEYS) {
			expect(trayCatalogs[locale][key], key).toBe(renderer[key]);
		}
	});
});
