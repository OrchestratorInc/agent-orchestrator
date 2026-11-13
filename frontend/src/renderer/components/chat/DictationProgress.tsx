import { useTranslation } from "react-i18next";

/**
 * The bar alone: it fills to the bytes received when the size is known and
 * sweeps when it is not (runtime setup has nothing to measure).
 */
export function DictationProgressBar({ done, total, label }: { done: number; total: number; label: string }) {
	const known = total > 0;
	const percent = known ? Math.min(100, Math.floor((done / total) * 100)) : 0;
	// Keyed apart so the fill starts from its value, not from the sweep's width.
	return known ? (
		<div
			key="determinate"
			className="ao-install-progress"
			role="progressbar"
			aria-label={label}
			aria-valuemin={0}
			aria-valuemax={100}
			aria-valuenow={percent}
		>
			<div
				className="ao-install-progress__bar ao-install-progress__bar--determinate"
				style={{ width: `${percent}%`, transition: "width 500ms linear" }}
			/>
		</div>
	) : (
		<div key="indeterminate" className="ao-install-progress" role="progressbar" aria-label={label}>
			<div className="ao-install-progress__bar" />
		</div>
	);
}

/** "311 of 740 MB" while the size is known, "Setting up…" while it is not. */
export function useDictationProgressText(done: number, total: number): string {
	const { t } = useTranslation();
	return total > 0
		? t("dictation.upgrade.progress", { done: Math.round(done / 1_000_000), total: Math.round(total / 1_000_000) })
		: t("dictation.upgrade.settingUp");
}

/** Stage text, byte count and bar on separate lines, for Settings. */
export function DictationProgress({ stage, done, total }: { stage: string; done: number; total: number }) {
	const text = useDictationProgressText(done, total);
	return (
		<div className="min-w-0 space-y-2">
			<div className="flex items-baseline justify-between gap-4 text-control">
				<p className="min-w-0 truncate text-settings-muted" role="status" title={stage}>
					{stage}
				</p>
				{total > 0 && <span className="shrink-0 tabular-nums text-muted-foreground">{text}</span>}
			</div>
			<DictationProgressBar done={done} total={total} label={stage} />
		</div>
	);
}
