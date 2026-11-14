import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AudioLines, ChevronRight, Download, Laptop, Search } from "lucide-react";
import { apiClient } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import { Button } from "../ui/button";
import { Pressable } from "../ui/pressable";
import { dictationClient, nativeDictationAvailable } from "../../lib/dictation-client";
import {
	acknowledgeDictationChoice,
	cancelDictationInstall,
	dictationChooserKey,
	DictationInstallCancelled,
	installDictationModel,
	publishDictationStatus,
	useDictationInstall,
} from "../../lib/dictation-install";
import { DictationProgressBar, useDictationPercent } from "./DictationProgress";
import { Dialog, DialogClose, DialogContent, DialogDescription, DialogOverlay, DialogTitle } from "../ui/dialog";
import { CloseButton } from "../ui/close-button";
import { cn } from "../../lib/utils";
import {
	centeredOnboardingDialogClass,
	onboardingPanelBodyClass,
	onboardingPanelDescriptionClass,
	onboardingPanelTitleClass,
} from "../../lib/onboarding-ui";

export const dictationUpgradeKey = dictationChooserKey;
const recommendedModel = "phonon-2.5";
const accurateModel = "handy-computer/parakeet-tdt-0.6b-v3-gguf";
const nativeModel = "native-macos";

type ModelOption = { installed: boolean; available: boolean; bytes: number };

function DictationRow({
	icon,
	label,
	badge,
	hint,
	action,
	disabled,
	chevron,
	installing,
	onClick,
}: {
	icon: ReactNode;
	label: string;
	badge?: string;
	hint?: string;
	action: string;
	disabled?: boolean;
	chevron?: boolean;
	/** Set while this row's model downloads: the row becomes its own progress. */
	installing?: { fraction: number; cancelling: boolean; onCancel: () => void };
	onClick: () => void;
}) {
	if (installing) return <DictationInstallingRow icon={icon} label={label} {...installing} />;
	return (
		<Pressable
			className="group flex w-full items-center gap-4 rounded-lg bg-interactive-hover px-4 py-3 text-left hover:bg-interactive-active disabled:pointer-events-none disabled:opacity-50"
			aria-label={action}
			disabled={disabled}
			onClick={onClick}
		>
			<span className="flex shrink-0 text-muted-foreground group-hover:text-foreground">{icon}</span>
			<span className="min-w-0 flex-1">
				<span className="block text-[14px] font-medium text-foreground">{label}</span>
				{hint && <span className="block text-pretty text-[12px] leading-5 text-muted-foreground">{hint}</span>}
			</span>
			{badge && <span className="shrink-0 text-[12px] leading-5 text-muted-foreground">{badge}</span>}
			{chevron && (
				<ChevronRight
					className="size-4 shrink-0 text-muted-foreground group-hover:text-foreground"
					aria-hidden="true"
				/>
			)}
		</Pressable>
	);
}

function DictationInstallingRow({
	icon,
	label,
	fraction,
	cancelling,
	onCancel,
}: {
	icon: ReactNode;
	label: string;
	fraction: number;
	cancelling: boolean;
	onCancel: () => void;
}) {
	const { t } = useTranslation();
	const percent = useDictationPercent(fraction);
	return (
		<div className="flex w-full items-center gap-4 rounded-lg bg-interactive-hover px-4 py-3">
			<span className="flex shrink-0 text-muted-foreground">{icon}</span>
			<div className="min-w-0 flex-1 space-y-2.5">
				<div className="flex items-baseline justify-between gap-3">
					<span className="truncate text-[14px] font-medium text-foreground">{label}</span>
					<span className="shrink-0 text-[12px] leading-5 tabular-nums text-muted-foreground" role="status">
						{percent}
					</span>
				</div>
				<DictationProgressBar fraction={fraction} label={label} />
			</div>
			<Button variant="ghost" size="sm" disabled={cancelling} onClick={onCancel}>
				{t("dictation.cancel")}
			</Button>
		</div>
	);
}

export function useDictationUpgrade(ownerKey?: string) {
	const { t } = useTranslation();
	const [open, setOpenState] = useState(false);
	const openRef = useRef(false);
	const setOpen = (next: boolean) => {
		openRef.current = next;
		setOpenState(next);
	};
	const [phase, setPhase] = useState<"choose" | "install" | "cancel">("choose");
	const [active, setActive] = useState("");
	const [options, setOptions] = useState<{
		native: boolean;
		models: Record<string, ModelOption>;
		downloading: string;
	}>({
		native: false,
		models: {},
		downloading: "",
	});
	const install = useDictationInstall();
	const target = useRef(recommendedModel);
	const [error, setError] = useState<string>();
	const start = useRef<() => void>(() => {});
	const checking = useRef(false);
	const generation = useRef(0);
	const operation = useRef(0);
	useEffect(() => {
		openRef.current = false;
		setOpenState(false);
		setPhase("choose");
		setActive("");
		checking.current = false;
		return () => {
			generation.current += 1;
			operation.current += 1;
			openRef.current = false;
		};
	}, [ownerKey]);
	const toast = (title: string, body: string | undefined, tone: "success" | "error") =>
		useUiStore.getState().showGlobalToast(title, body, { tone, dedupeKey: "dictation-install" });
	const requestStart = async (onStart: () => void) => {
		if (checking.current || open) return;
		checking.current = true;
		const current = generation.current;
		try {
			const { data, error } = await apiClient.GET("/api/v1/dictation");
			if (current !== generation.current) return;
			if (error || !data) throw new Error(t("dictation.loadFailed"));
			publishDictationStatus(data);
			let seen = false;
			try {
				seen = localStorage.getItem(dictationUpgradeKey) === "seen";
			} catch {
				/* Show the chooser. */
			}
			const usable = data.models.some((model) => model.id === data.selected && model.installed && model.available);
			// Dictation cannot start while a download runs; the mic button shows how far along it is.
			if (seen && usable && !data.installing) {
				onStart();
				return;
			}
			const models: Record<string, ModelOption> = {};
			for (const id of [recommendedModel, accurateModel]) {
				const found = data.models.find((model) => model.id === id);
				if (found)
					models[id] = {
						installed: found.installed,
						available: found.available,
						bytes: found.downloadBytes ?? 0,
					};
			}
			// Some other model is downloading (started from Settings): there is nothing to choose here.
			if (data.installing && !(data.installing in models)) return;
			const next = {
				native: nativeDictationAvailable() && data.models.some((model) => model.id === nativeModel && model.available),
				models,
				downloading: data.installing ?? "",
			};
			setOptions(next);
			start.current = onStart;
			setError(undefined);
			setPhase("choose");
			setOpen(true);
			// An install already under way (started earlier, or from Settings) is shown as that row's progress.
			if (next.downloading in models) void choose(next.downloading, next);
		} catch {
			if (current === generation.current) onStart();
		} finally {
			if (current === generation.current) checking.current = false;
		}
	};
	const change = async (action: "install" | "select", model: string) => {
		const { data, error } = await apiClient.POST("/api/v1/dictation", {
			body: { action, model },
		});
		if (error || !data) throw new Error(error?.message ?? t("dictation.updateFailed"));
		return data;
	};
	const choose = async (model: string, from = options) => {
		const native = model === nativeModel;
		target.current = model;
		const current = ++operation.current;
		const valid = () => current === operation.current;
		const needsInstall = !native && !from.models[model]?.installed;
		setError(undefined);
		// Switching to something already on this machine is instant; only a download, or the one-time
		// macOS permission prompts, earn a progress state.
		if (needsInstall || native) {
			setPhase("install");
			setActive(model);
		}
		try {
			if (native) {
				// Both macOS prompts (speech recognition, microphone) are settled here, once, so they never
				// interrupt the first dictation. When access was already granted this returns at once.
				const access = await dictationClient.authorize();
				if (!valid()) return;
				if (!access.granted) throw new Error(access.message ?? t("dictation.updateFailed"));
			}
			if (needsInstall) {
				let initial;
				if (from.downloading === model) {
					const response = await apiClient.GET("/api/v1/dictation");
					if (response.error || !response.data) throw new Error(t("dictation.loadFailed"));
					initial = response.data;
				} else {
					initial = await change("install", model);
				}
				if (!valid()) return;
				// The download belongs to the app, so it carries on, and selects the model, even if this
				// dialog is closed or its chat is left.
				await installDictationModel(model, initial, {
					loadFailed: t("dictation.loadFailed"),
					updateFailed: t("dictation.updateFailed"),
				});
				if (!openRef.current) toast(t("dictation.upgrade.ready"), undefined, "success");
			} else {
				await change("select", model);
				if (!valid()) return;
				acknowledgeDictationChoice();
			}
			if (!valid()) return;
			setPhase("choose");
			setActive("");
			// Left while it downloaded: the mic button and a toast said so, and nothing starts listening unasked.
			if (!openRef.current) return;
			// Whatever was chosen is now ready: get out of the way and start listening.
			setOpen(false);
			start.current();
		} catch (cause) {
			if (cause instanceof DictationInstallCancelled) {
				if (valid()) {
					setPhase("choose");
					setActive("");
				}
				return;
			}
			const message = cause instanceof Error ? cause.message : t("dictation.updateFailed");
			if (!openRef.current) toast(t("dictation.updateFailed"), message, "error");
			if (!valid()) return;
			setPhase("choose");
			setActive("");
			setOptions((previous) => ({ ...previous, downloading: "" }));
			setError(message);
		}
	};
	const cancel = async () => {
		const current = ++operation.current;
		setPhase("cancel");
		try {
			await cancelDictationInstall();
			if (current !== operation.current) return;
			setOptions((previous) => ({ ...previous, downloading: "" }));
			setPhase("choose");
			setActive("");
		} catch (cause) {
			if (current !== operation.current) return;
			setPhase("choose");
			setActive("");
			setError(cause instanceof Error ? cause.message : t("dictation.updateFailed"));
		}
	};
	const modelIcon = (model: ModelOption) =>
		model.installed ? (
			<AudioLines className="size-6" aria-hidden="true" strokeWidth={1.6} />
		) : (
			<Download className="size-6" aria-hidden="true" strokeWidth={1.6} />
		);
	const busy = phase !== "choose";
	// Closing never blocks the user. A download carries on in the background; anything else still waiting on
	// the daemon is abandoned, so a late reply cannot start dictation.
	const dismiss = () => {
		if (phase !== "install" || active === nativeModel) {
			operation.current += 1;
			setPhase("choose");
			setActive("");
		}
		setOpen(false);
	};
	const installingProps = (model: string) => {
		if (active !== model) return undefined;
		return {
			fraction: install.model === model ? install.fraction : 0,
			cancelling: phase === "cancel",
			onCancel: () => void cancel(),
		};
	};
	return {
		requestStart,
		dialog: (
			<Dialog
				open={open}
				onOpenChange={(next) => {
					if (!next) dismiss();
				}}
			>
				<DialogContent
					// Opens and closes instantly: no fade or scale, so choosing never waits on an animation.
					className={cn(
						centeredOnboardingDialogClass,
						"data-[state=open]:animate-none data-[state=closed]:animate-none",
					)}
					overlay={<DialogOverlay className="data-[state=open]:animate-none data-[state=closed]:animate-none" />}
					showCloseButton={false}
				>
					<DialogClose asChild>
						<CloseButton floating aria-label={t("common.close")} />
					</DialogClose>
					<DialogTitle className={cn(onboardingPanelTitleClass, "pr-12")}>{t("dictation.upgrade.title")}</DialogTitle>
					<DialogDescription className={onboardingPanelDescriptionClass}>
						{t("dictation.upgrade.description")}
					</DialogDescription>
					<div className={cn(onboardingPanelBodyClass, "gap-3")}>
						<div className="space-y-2">
							{options.native && (
								<DictationRow
									icon={<Laptop className="size-6" aria-hidden="true" strokeWidth={1.6} />}
									label={t("dictation.upgrade.nativeName")}
									hint={t("dictation.upgrade.nativeDetail")}
									action={t("dictation.upgrade.native")}
									disabled={busy}
									onClick={() => void choose(nativeModel)}
								/>
							)}
							{options.models[recommendedModel] && (
								<DictationRow
									icon={modelIcon(options.models[recommendedModel])}
									label="Phonon 2.5"
									badge={t("dictation.upgrade.recommended")}
									hint={t(
										options.models[recommendedModel].installed
											? "dictation.upgrade.installed"
											: "dictation.upgrade.download",
										{ size: Math.round(options.models[recommendedModel].bytes / 1_000_000) },
									)}
									action={t("dictation.upgrade.install")}
									disabled={busy || !options.models[recommendedModel].available}
									installing={installingProps(recommendedModel)}
									onClick={() => void choose(recommendedModel)}
								/>
							)}
							{options.models[accurateModel] && (
								<DictationRow
									icon={modelIcon(options.models[accurateModel])}
									label="Parakeet TDT 0.6B v3"
									badge={t("dictation.upgrade.accurate")}
									hint={t(
										options.models[accurateModel].installed
											? "dictation.upgrade.installed"
											: "dictation.upgrade.download",
										{ size: Math.round(options.models[accurateModel].bytes / 1_000_000) },
									)}
									action={t("dictation.upgrade.useAccurate")}
									disabled={busy || !options.models[accurateModel].available}
									installing={installingProps(accurateModel)}
									onClick={() => void choose(accurateModel)}
								/>
							)}
							<DictationRow
								chevron
								icon={<Search className="size-6" aria-hidden="true" strokeWidth={1.6} />}
								label={t("dictation.upgrade.explore")}
								hint={t("dictation.upgrade.exploreDetail")}
								action={t("dictation.upgrade.explore")}
								disabled={busy}
								onClick={() => {
									acknowledgeDictationChoice();
									dismiss();
									useUiStore.getState().openGlobalSettings("dictation");
								}}
							/>
						</div>
						{error && (
							<p className="text-caption leading-4 text-error" role="alert">
								{error}
							</p>
						)}
					</div>
				</DialogContent>
			</Dialog>
		),
	};
}
