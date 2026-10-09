import { useTranslation } from "react-i18next";
import type { components } from "../../../api/schema";
import { AppLink } from "../AppLink";
import { CopyButton } from "../chat/CopyButton";

type UninstallGuide = components["schemas"]["AgentUninstallGuide"];

const windowsPath = (path: string) => path.includes("%");

/** PowerShell does not expand %VAR%; rewrite to $env:VAR and quote. */
function powershellPath(path: string): string {
	return `"${path.replace(/%([A-Z_]+)%/g, "$$env:$1")}"`;
}

/** Keep ~ and globs unquoted so the shell expands them; quote only paths with spaces. */
function posixPath(path: string): string {
	return /\s/.test(path) ? `"${path}"` : path;
}

/** One command that removes every path, in the shell the paths belong to. */
export function removalCommand(paths: string[]): string {
	if (paths.length === 0) return "";
	if (paths.some(windowsPath)) return paths.map((path) => `Remove-Item -Recurse -Force ${powershellPath(path)}`).join("; ");
	return `rm -rf ${paths.map(posixPath).join(" ")}`;
}

function CommandLine({ command, label }: { command: string; label: string }) {
	return <div className="flex min-w-0 items-start gap-1">
		<code className="min-w-0 flex-1 break-all rounded-sm bg-(--color-bg-settings-input) px-1.5 py-1 font-mono text-[11px] text-settings-label">{command}</code>
		<CopyButton compact label={label} text={command} />
	</div>;
}

/** Steps to remove a vendor install that AO does not remove itself. AO never runs them. */
export function HarnessUninstallGuide({ agent, guide, fallbackDocsUrl }: { agent: string; guide: UninstallGuide; fallbackDocsUrl?: string }) {
	const { t } = useTranslation();
	const docsUrl = guide.docsUrl || fallbackDocsUrl;
	const removeProgram = removalCommand(guide.programPaths);
	const removeData = removalCommand(guide.userDataPaths ?? []);
	return <div className="space-y-2 text-xs text-settings-muted">
		<p className="font-medium text-settings-label">{t("settings.harness.removeGuide.title", { agent })}</p>
		{guide.command ? <div className="space-y-1">
			<p>{t("settings.harness.removeGuide.runUninstaller")}</p>
			<CommandLine command={guide.command} label={t("settings.harness.copyCommand")} />
		</div> : null}
		{removeProgram ? <div className="space-y-1">
			<p>{t(guide.command ? "settings.harness.removeGuide.orRemoveProgram" : "settings.harness.removeGuide.removeProgram")}</p>
			<CommandLine command={removeProgram} label={t("settings.harness.copyCommand")} />
		</div> : null}
		{guide.editsShellProfile ? <p>{t("settings.harness.removeGuide.shellProfile")}</p> : null}
		{removeData ? <div className="space-y-1">
			<p>{t("settings.harness.removeGuide.userData")}</p>
			<CommandLine command={removeData} label={t("settings.harness.copyCommand")} />
		</div> : null}
		{!guide.documented ? <p>{t("settings.harness.removeGuide.fromInstaller")}</p> : null}
		{docsUrl ? <AppLink className="text-accent underline-offset-2 hover:underline" href={docsUrl} rel="noopener noreferrer" target="_blank">{t("settings.harness.removeGuide.guide")}</AppLink> : null}
	</div>;
}
