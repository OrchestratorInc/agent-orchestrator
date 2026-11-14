import { useTranslation } from "react-i18next";

/** The bar alone. It only ever fills: the daemon's progress runs from start to finish and never goes back. */
export function DictationProgressBar({ fraction, label }: { fraction: number; label: string }) {
	const percent = Math.round(Math.min(1, Math.max(0, fraction)) * 100);
	return (
		<div
			className="ao-install-progress"
			role="progressbar"
			aria-label={label}
			aria-valuemin={0}
			aria-valuemax={100}
			aria-valuenow={percent}
		>
			<div className="ao-install-progress__bar" style={{ width: `${percent}%` }} />
		</div>
	);
}

/** "42%", formatted for the app's language. */
export function useDictationPercent(fraction: number): string {
	const { i18n } = useTranslation();
	return new Intl.NumberFormat(i18n.language, { style: "percent", maximumFractionDigits: 0 }).format(
		Math.min(1, Math.max(0, fraction)),
	);
}

/** Stage text, percentage and bar on separate lines, for Settings. */
export function DictationProgress({ stage, fraction }: { stage: string; fraction: number }) {
	const percent = useDictationPercent(fraction);
	return (
		<div className="min-w-0 space-y-2">
			<div className="flex items-baseline justify-between gap-4 text-control">
				<p className="min-w-0 truncate text-settings-muted" role="status" title={stage}>
					{stage}
				</p>
				<span className="shrink-0 tabular-nums text-muted-foreground">{percent}</span>
			</div>
			<DictationProgressBar fraction={fraction} label={stage} />
		</div>
	);
}
