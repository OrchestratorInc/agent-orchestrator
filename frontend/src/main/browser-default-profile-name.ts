// The first-run browser profile is named in the user's interface language. The
// main process cannot load the renderer catalogs, so the name comes from the
// small generated catalog (scripts/sync-tray-catalog.mjs).
import type { AppLocale } from "../shared/ui-locale";
import { trayCatalogs } from "./tray-catalog.generated";

export function defaultBrowserProfileName(locale: AppLocale): string {
	return (trayCatalogs[locale] ?? trayCatalogs.en)["browser.profile.defaultName"];
}
