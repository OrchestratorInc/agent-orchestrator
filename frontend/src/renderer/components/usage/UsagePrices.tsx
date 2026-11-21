import { Plus, X } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { useRestoreFocusOnClose } from "../../hooks/useRestoreFocusOnClose";
import { useSaveUsagePrices, useUsagePrices, type UsagePriceOverride } from "../../hooks/useUsageReport";
import { cn } from "../../lib/utils";
import { Button } from "../ui/button";
import { CloseButton } from "../ui/close-button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogDescription,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Input } from "../ui/input";

type Draft = Required<Omit<UsagePriceOverride, "modelId">> & { key: number; modelId: string };
const RATE_FIELDS = ["inputUsdPerMillion", "outputUsdPerMillion", "cacheReadUsdPerMillion", "cacheWriteUsdPerMillion"] as const;
const RATE_LABELS = {
	inputUsdPerMillion: "usage.type.input",
	outputUsdPerMillion: "usage.type.output",
	cacheReadUsdPerMillion: "usage.type.cacheRead",
	cacheWriteUsdPerMillion: "usage.type.cacheWrite",
} as const satisfies Record<(typeof RATE_FIELDS)[number], string>;

function hasValues(row: Draft): boolean {
	return row.priceAs.trim() !== "" || RATE_FIELDS.some((field) => row[field].trim() !== "");
}

let nextKey = 0;
function draftOf(override: Partial<UsagePriceOverride>): Draft {
	nextKey += 1;
	return {
		key: nextKey,
		modelId: override.modelId ?? "",
		priceAs: override.priceAs ?? "",
		inputUsdPerMillion: override.inputUsdPerMillion ?? "",
		outputUsdPerMillion: override.outputUsdPerMillion ?? "",
		cacheReadUsdPerMillion: override.cacheReadUsdPerMillion ?? "",
		cacheWriteUsdPerMillion: override.cacheWriteUsdPerMillion ?? "",
	};
}

export function UsagePricesDialog({
	open,
	onOpenChange,
	model,
	seenModels,
}: {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	model?: string;
	seenModels: readonly string[];
}) {
	const { t } = useTranslation();
	const prices = useUsagePrices();
	const save = useSaveUsagePrices();
	const restoreFocusOnClose = useRestoreFocusOnClose(open);
	const [drafts, setDrafts] = useState<Draft[]>([]);
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	const [checked, setChecked] = useState(false);
	const listId = useId();
	const loaded = prices.data;

	useEffect(() => {
		if (!open || !loaded) return;
		const rows = loaded.overrides.map(draftOf);
		if (model && !rows.some((row) => row.modelId === model)) rows.push(draftOf({ modelId: model }));
		if (rows.length === 0) rows.push(draftOf({}));
		setDrafts(rows);
		setError(null);
		setChecked(false);
	}, [open, loaded, model]);

	const update = (key: number, field: Exclude<keyof Draft, "key">, value: string) =>
		setDrafts((rows) => rows.map((row) => (row.key === key ? { ...row, [field]: value } : row)));
	const unnamed = (row: Draft) => row.modelId.trim() === "" && hasValues(row);
	const missingModel = checked && drafts.some(unnamed);
	const submit = async () => {
		if (drafts.some(unnamed)) {
			setChecked(true);
			return;
		}
		setBusy(true);
		setError(null);
		try {
			await save(
				drafts
					.filter((row) => row.modelId.trim() !== "")
					.map((row): UsagePriceOverride => (row.priceAs.trim() !== ""
						? { modelId: row.modelId, priceAs: row.priceAs }
						: {
							modelId: row.modelId,
							inputUsdPerMillion: row.inputUsdPerMillion,
							outputUsdPerMillion: row.outputUsdPerMillion,
							cacheReadUsdPerMillion: row.cacheReadUsdPerMillion,
							cacheWriteUsdPerMillion: row.cacheWriteUsdPerMillion,
						})),
			);
			onOpenChange(false);
		} catch (cause) {
			setError(t("usage.prices.saveFailed", { error: cause instanceof Error ? cause.message : String(cause) }));
		} finally {
			setBusy(false);
		}
	};

	return (
		<Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
			<DialogContent
				showCloseButton={false}
				className={cn(settingsDialogContentClass, "w-[min(880px,calc(100vw-24px))]")}
				onCloseAutoFocus={restoreFocusOnClose}
			>
				<DialogClose asChild>
					<CloseButton floating disabled={busy} aria-label={t("common.close")} />
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">{t("usage.prices.title")}</DialogTitle>
					<DialogDescription className="text-control leading-5 text-settings-muted">{t("usage.prices.description")}</DialogDescription>
				</div>
				<div className={cn(settingsDialogBodyClass, "pt-0")}>
					<datalist id={`${listId}-seen`}>
						{seenModels.map((id) => <option key={id} value={id} />)}
					</datalist>
					<datalist id={`${listId}-catalog`}>
						{(loaded?.models ?? []).map((id) => <option key={id} value={id} />)}
					</datalist>
					<table className="w-full table-fixed border-collapse text-control">
						<thead>
							<tr className="text-xs text-muted-foreground">
								<th scope="col" className="w-[24%] pb-1.5 text-start font-normal">{t("usage.prices.model")}</th>
								<th scope="col" className="w-[24%] pb-1.5 ps-2 text-start font-normal">{t("usage.prices.priceAs")}</th>
								{RATE_FIELDS.map((field) => (
									<th key={field} scope="col" className="pb-1.5 ps-2 text-start font-normal">{t(RATE_LABELS[field])}</th>
								))}
								<th scope="col" className="w-8" aria-hidden="true" />
							</tr>
						</thead>
						<tbody>
							{drafts.map((row) => {
								const mapped = row.priceAs.trim() !== "";
								const name = row.modelId || t("usage.prices.newModel");
								return (
									<tr key={row.key}>
										<td className="py-1">
											<Input
												className="h-8 font-mono text-xs"
												aria-label={t("usage.prices.model")}
												placeholder={t("usage.prices.modelPlaceholder")}
												list={`${listId}-seen`}
												maxLength={256}
												aria-invalid={checked && unnamed(row) ? true : undefined}
												value={row.modelId}
												disabled={busy}
												onChange={(event) => update(row.key, "modelId", event.target.value)}
											/>
										</td>
										<td className="py-1 ps-2">
											<Input
												className="h-8 font-mono text-xs"
												aria-label={t("usage.prices.priceAsFor", { model: name })}
												placeholder={t("usage.prices.priceAsPlaceholder")}
												list={`${listId}-catalog`}
												maxLength={256}
												value={row.priceAs}
												disabled={busy}
												onChange={(event) => update(row.key, "priceAs", event.target.value)}
											/>
										</td>
										{RATE_FIELDS.map((field) => (
											<td key={field} className="py-1 ps-2">
												<Input
													className="h-8 text-xs tabular-nums"
													aria-label={t("usage.prices.rateFor", { rate: t(RATE_LABELS[field]), model: name })}
													placeholder={mapped ? "" : field.startsWith("cache") ? t("usage.prices.sameAsInput") : "0.00"}
													inputMode="decimal"
													value={row[field]}
													disabled={busy || mapped}
													onChange={(event) => update(row.key, field, event.target.value)}
												/>
											</td>
										))}
										<td className="py-1 ps-1 text-end">
											<Button
												type="button"
												variant="destructive-ghost"
												size="icon-xs"
												aria-label={t("usage.prices.remove", { model: name })}
												disabled={busy}
												onClick={() => setDrafts((rows) => rows.filter((other) => other.key !== row.key))}
											>
												<X aria-hidden="true" />
											</Button>
										</td>
									</tr>
								);
							})}
						</tbody>
					</table>
					<div className="flex items-center justify-between gap-3">
						<Button type="button" variant="quiet" size="sm" disabled={busy} onClick={() => setDrafts((rows) => [...rows, draftOf({})])}>
							<Plus aria-hidden="true" />
							{t("usage.prices.add")}
						</Button>
						<span className="text-xs text-muted-foreground">{t("usage.prices.unit")}</span>
					</div>
					{missingModel ? <p role="alert" className="text-caption leading-4 text-error">{t("usage.prices.modelRequired")}</p> : null}
					{error ? <p role="alert" className="text-caption leading-4 text-error">{error}</p> : null}
				</div>
				<div className={settingsDialogFooterClass}>
					<DialogClose asChild>
						<Button type="button" variant="secondary" disabled={busy}>{t("usage.prices.cancel")}</Button>
					</DialogClose>
					<Button type="button" variant="primary" disabled={busy || !loaded} onClick={() => void submit()}>
						{t("usage.prices.save")}
					</Button>
				</div>
			</DialogContent>
		</Dialog>
	);
}
