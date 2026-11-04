import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

/** Where AO's local logs live (daemon.log, main.log) and a way to open them. */
export function LogsFolderSection() {
	const { t } = useTranslation();
	const [directory, setDirectory] = useState<string | null>(null);
	const [error, setError] = useState<string | null>(null);

	useEffect(() => {
		let active = true;
		// A partial bridge (tests, web preview) without the call falls back to the
		// generic description instead of throwing.
		void Promise.resolve().then(() => aoBridge.app.getLogsDirectory()).then((next) => {
			if (active) setDirectory(next);
		}).catch(() => undefined);
		return () => {
			active = false;
		};
	}, []);

	const openFolder = async () => {
		setError(null);
		try {
			await aoBridge.app.openLogsFolder();
		} catch {
			setError(t("settings.logs.openFailed"));
		}
	};

	return (
		<SettingsSection title={t("settings.logs.title")} grouped>
			<SettingsRow
				label={t("settings.logs.label")}
				description={error ? (
					<span role="alert" className="text-error">{error}</span>
				) : directory ? t("settings.logs.description", { path: directory }) : t("settings.logs.descriptionUnknown")}
			>
				<Button type="button" size="sm" variant="ghost" onClick={() => void openFolder()}>
					{t("settings.logs.open")}
				</Button>
			</SettingsRow>
		</SettingsSection>
	);
}
