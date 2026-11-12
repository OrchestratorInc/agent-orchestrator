import { FolderOpen } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { aoBridge } from "../../lib/bridge";
import { RemoteDirectoryPicker } from "../RemoteDirectoryPicker";

/**
 * The worktree-root row's value control: an absolute-path text field plus a
 * "choose folder" button.
 *
 * The local desktop app asks Electron for a native folder dialog. A project
 * owned by another host has no native dialog here, so it browses that daemon's
 * filesystem through /api/v1/fs/dirs instead. Cloud projects never render this
 * control at all (see ProjectSettingsEditor's capabilities).
 */
export function WorktreeRootField({
	value,
	onChange,
	hostId,
	hostLabel,
	hostConnected,
	disabled,
	invalid,
}: {
	value: string;
	onChange: (value: string) => void;
	hostId?: string;
	hostLabel?: string;
	hostConnected: boolean;
	disabled?: boolean;
	invalid?: boolean;
}) {
	const { t } = useTranslation();
	const [browsing, setBrowsing] = useState(false);
	const remote = Boolean(hostId);
	const canBrowse = !disabled && hostConnected && (remote || Boolean(aoBridge?.app?.chooseDirectory));
	return (
		<>
			<div className="flex min-w-0 items-center gap-2">
				<input
					id="worktreeRoot"
					aria-label={t("settings.project.worktreeRoot")}
					aria-invalid={invalid || undefined}
					className={`settings-inline-edit-input min-w-0 flex-1${invalid ? " text-error" : ""}`}
					value={value}
					placeholder={t("settings.project.worktreeRootPlaceholder")}
					disabled={disabled}
					onChange={(event) => onChange(event.target.value)}
					spellCheck={false}
					autoComplete="off"
				/>
				<button
					type="button"
					className="icon-button shrink-0"
					aria-label={t("settings.project.chooseWorktreeRoot")}
					disabled={!canBrowse}
					onClick={() => {
						if (remote) {
							setBrowsing(true);
							return;
						}
						void (async () => {
							const selected = await aoBridge.app.chooseDirectory({
								title: t("settings.project.chooseWorktreeRoot"),
								defaultPath: value.trim() || undefined,
							});
							if (selected) onChange(selected);
						})();
					}}
				>
					<FolderOpen className="size-4" aria-hidden="true" />
				</button>
			</div>
			{remote && browsing && hostId ? (
				<RemoteDirectoryPicker
					hostId={hostId}
					hostLabel={hostLabel ?? hostId}
					connected={hostConnected}
					initialPath={value.trim()}
					onClose={() => setBrowsing(false)}
					onChoose={(path) => {
						setBrowsing(false);
						onChange(path);
					}}
				/>
			) : null}
		</>
	);
}
