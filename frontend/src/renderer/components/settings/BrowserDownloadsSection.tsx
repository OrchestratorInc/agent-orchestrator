import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { useBrowserDownloads } from "../../hooks/useBrowserDownloads";
import { BrowserDownloadsList } from "../BrowserDownloadsList";
import { Button } from "../ui/button";
import { SettingsSearch } from "./SettingsSearch";
import { SettingsSection } from "./SettingsSection";

export function BrowserDownloadsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const { downloads, error, action, clear } = useBrowserDownloads();
	const [query, setQuery] = useState("");
	const filtered = useMemo(() => {
		const normalized = query.trim().toLocaleLowerCase();
		return normalized ? downloads.filter((download) => download.fileName.toLocaleLowerCase().includes(normalized)) : downloads;
	}, [downloads, query]);
	const hasFinished = downloads.some((download) => download.status !== "progressing" && download.status !== "paused");

	return (
		<SettingsSection
			title={t("settings.downloads")}
			sectionId="downloads"
			titleHidden={titleHidden}
			grouped
			toolbar={
				<SettingsSearch
					aria-label={t("browser.downloads.search")}
					placeholder={t("browser.downloads.search")}
					value={query}
					onChange={setQuery}
					trailing={
						<Button
							className="text-settings-muted hover:text-settings-label"
							disabled={!hasFinished}
							onClick={() => void clear()}
							size="sm"
							type="button"
							variant="ghost"
						>
							{t("browser.downloads.clearAll")}
						</Button>
					}
				/>
			}
		>
			<BrowserDownloadsList downloads={filtered} error={error} onAction={(id, nextAction) => void action(id, nextAction)} />
		</SettingsSection>
	);
}
