import { ChevronDown, Cloud, LoaderCircle } from "@/components/app-icons";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import type { CloudHarnessDetails } from "../../../hooks/useCloudHarnessVersions";
import { versionLabel } from "../../../hooks/useHarnessUpdates";
import { cn } from "../../../lib/utils";
import { MiddleTruncatedText } from "../../MiddleTruncatedText";
import { Button } from "../../ui/button";
import { HarnessStatusDot } from "./harness-ui";

/** AO Cloud's side of one harness, as its row on the Harness page shows it. */
export interface HarnessCloudState {
	/** Undefined while loading, or when the control plane could not say. */
	details?: CloudHarnessDetails;
	loading: boolean;
	/** An update or a return to the AO-tested release is being saved. */
	pending: boolean;
	/** Why the last update or return failed to save. */
	error?: string;
}

/** Whether AO Cloud's side of a harness reads as a problem the caller can act on. */
export function cloudNeedsAttention(cloud?: HarnessCloudState): boolean {
	return Boolean(cloud?.details?.editable && (cloud.details.failed || cloud.error));
}

/**
 * AO Cloud as a second, compact line inside a harness's installation row: the
 * release new Cloud sessions run, quiet unless the caller can update it or an
 * update failed. The AO-tested release, the Chat adapter, a failure's output
 * and the way back to the AO-tested release open beneath it on demand. Its own
 * Update button shows only when the row's main action is not an update
 * (the harness is not installed here); otherwise that one button covers both.
 */
export function HarnessCloudLine({ state, standaloneUpdate, onUpdate, onReset }: {
	state: HarnessCloudState;
	standaloneUpdate: boolean;
	onUpdate: () => void;
	onReset: () => void;
}) {
	const { t } = useTranslation();
	const [expanded, setExpanded] = useState(false);
	const detailsId = useId();
	const { details, pending, error } = state;
	const job = details?.entry.job;
	const failed = !pending && Boolean(details?.editable && (details.failed || error));
	return <div data-harness-cloud-line="" className="flex min-w-0 basis-full flex-col gap-2">
		{/* One line, the same height loading or loaded, so nothing below it moves when the versions arrive. */}
		<div className="flex min-h-5 min-w-0 flex-wrap items-center gap-x-2 gap-y-1 text-xs">
			<button
				type="button"
				className="inline-flex shrink-0 items-center gap-1.5 rounded-sm text-settings-muted outline-none hover:text-settings-label focus-visible:ring-2 focus-visible:ring-ring disabled:hover:text-settings-muted"
				aria-expanded={details ? expanded : undefined}
				aria-controls={details ? detailsId : undefined}
				disabled={!details}
				onClick={() => setExpanded(!expanded)}
			>
				<Cloud className="size-3.5" aria-hidden="true" />
				<span>{t("settings.harness.cloud.label")}</span>
				{details ? <ChevronDown className={cn("size-3 transition-transform duration-200 motion-reduce:transition-none", expanded && "rotate-180")} aria-hidden="true" /> : null}
			</button>
			{details ? <MiddleTruncatedText className="whitespace-nowrap font-mono text-settings-label" text={versionLabel(details.currentVersion)} />
				: <span className="text-settings-muted">{state.loading ? t("settings.harness.cloud.loading") : t("settings.harness.versionUnavailable")}</span>}
			{pending ? <span role="status" className="inline-flex items-center gap-1 text-settings-muted"><LoaderCircle className="size-3 animate-spin" aria-hidden="true" />{t("settings.harness.updating")}</span>
				: details?.actionable ? <MiddleTruncatedText className="whitespace-nowrap font-medium text-settings-accent" text={versionLabel(details.latestVersion!)} format={(version) => t("settings.harness.versionAvailable", { version })} />
				: null}
			{failed ? <span className="inline-flex items-center gap-1.5 text-error"><HarnessStatusDot tone="error" />{t("settings.harness.cloud.updateFailed")}</span> : null}
			{standaloneUpdate && details?.actionable && !pending ? <Button type="button" size="xs" variant="secondary" onClick={onUpdate}>{t("settings.harness.update")}</Button> : null}
		</div>
		{error && !pending ? <p role="alert" className="break-words text-xs text-error">{error}</p> : null}
		{details && expanded ? <div id={detailsId} className="flex flex-col gap-1.5 rounded-md border border-(--color-border-settings-input) bg-(--color-bg-settings-input) p-2.5 text-xs text-settings-muted">
			<p>{details.onDefault ? t("settings.harness.cloud.runsTested", { version: versionLabel(details.currentVersion) })
				: t("settings.harness.cloud.runsNewer", { version: versionLabel(details.currentVersion), tested: versionLabel(details.defaultVersion) })}</p>
			{details.adapterVersion ? <p>{t("settings.harness.cloud.chatAdapter", { version: versionLabel(details.adapterVersion) })}</p> : null}
			{job?.status === "failed" ? <>
				<p className="text-error">{t("settings.harness.cloud.installFailed", { tested: versionLabel(details.defaultVersion) })}</p>
				{job.error ? <p className="whitespace-pre-wrap break-words text-error">{job.error}</p> : null}
				{job.output ? <pre className="settings-thin-scrollbar max-h-40 overflow-auto overscroll-contain whitespace-pre-wrap break-words font-mono">{job.output}</pre> : null}
			</> : null}
			{!details.editable ? <p>{t("settings.harness.cloud.readOnly")}</p> : null}
			{details.editable && !details.onDefault ? <div>
				<Button type="button" size="sm" variant="secondary" disabled={pending} onClick={onReset}>{t("settings.harness.cloud.useTested")}</Button>
			</div> : null}
		</div> : null}
	</div>;
}
