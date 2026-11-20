import type { TFunction } from "i18next";
import type { components } from "../../api/schema";

type InstallProgress = components["schemas"]["InstallProgress"];

/** The shared script runner's own download (attempt and retry wait), from the
 * daemon's structured progress fields. Undefined once the vendor script runs,
 * so its output takes over. */
export function installerProgress(progress: InstallProgress | undefined, t: TFunction): string | undefined {
	const attempt = progress?.downloadAttempt;
	const max = progress?.downloadAttempts;
	if (!attempt || !max) return undefined;
	const seconds = progress.retryInSeconds;
	return seconds
		? t("settings.harness.progress.retryingDownload", { seconds, attempt, max })
		: t("settings.harness.progress.downloadingInstaller", { attempt, max });
}
