import { useEffect, useRef, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { AudioLines, ChevronRight, Download, Laptop, Search } from "lucide-react";
import { apiClient } from "../../lib/api-client";
import { useUiStore } from "../../stores/ui-store";
import { Pressable } from "../ui/pressable";
import { dictationClient, nativeDictationAvailable } from "../../lib/dictation-client";
import {
	acknowledgeDictationChoice,
	dictationChooserKey,
	DictationInstallCancelled,
	installDictationModel,
	publishDictationStatus,
} from "../../lib/dictation-install";
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
const modelNames: Record<string, string> = {
	[recommendedModel]: "Phonon 2.5",
	[accurateModel]: "Parakeet TDT 0.6B v3",
};

type ModelOption = { installed: boolean; available: boolean; bytes: number };

function DictationRow({
	icon,
	label,
	badge,
	hint,
	action,
	disabled,
	chevron,
	onClick,
}: {
	icon: ReactNode;
	label: string;
	badge?: string;
	hint?: string;
	action: string;
	disabled?: boolean;
	chevron?: boolean;
	onClick: () => void;
}) {
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

/**
 * The first-use chooser. Choosing something already on this machine starts dictation. Choosing a download closes
 * the chooser at once: the download carries on in the background, the mic button shows how far along it is, and
 * the model is selected when it finishes.
 */
export function useDictationUpgrade(ownerKey?: string) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	// Set while the daemon or the macOS prompts are being waited on, so a second choice cannot start.
	const [busy, setBusy] = useState(false);
	const [options, setOptions] = useState<{ native: boolean; models: Record<string, ModelOption> }>({
		native: false,
		models: {},
	});
	const [error, setError] = useState<string>();
	const start = useRef<() => void>(() => {});
	const checking = useRef(false);
	const generation = useRef(0);
	const operation = useRef(0);
	useEffect(() => {
		setOpen(false);
		setBusy(false);
		checking.current = false;
		return () => {
			generation.current += 1;
			operation.current += 1;
		};
	}, [ownerKey]);
	const toast = (title: string, body: string | undefined, tone: "info" | "success" | "error") =>
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
			// Dictation cannot start while a model downloads; the mic button shows how far along it is.
			if (data.installing) return;
			let seen = false;
			try {
				seen = localStorage.getItem(dictationUpgradeKey) === "seen";
			} catch {
				/* Show the chooser. */
			}
			const usable = data.models.some((model) => model.id === data.selected && model.installed && model.available);
			if (seen && usable) {
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
			setOptions({
				native: nativeDictationAvailable() && data.models.some((model) => model.id === nativeModel && model.available),
				models,
			});
			start.current = onStart;
			setError(undefined);
			setBusy(false);
			setOpen(true);
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
	const choose = async (model: string) => {
		const native = model === nativeModel;
		const current = ++operation.current;
		const valid = () => current === operation.current;
		const needsInstall = !native && !options.models[model]?.installed;
		setError(undefined);
		setBusy(true);
		try {
			if (native) {
				// Both macOS prompts (speech recognition, microphone) are settled here, once, so they never
				// interrupt the first dictation. When access was already granted this returns at once.
				const access = await dictationClient.authorize();
				if (!valid()) return;
				if (!access.granted) throw new Error(access.message ?? t("dictation.updateFailed"));
			}
			if (needsInstall) {
				const initial = await change("install", model);
				if (!valid()) return;
				// The download belongs to the app, not to this dialog: it carries on, and selects the model,
				// whatever happens to the dialog or the chat.
				void installDictationModel(model, initial, {
					loadFailed: t("dictation.loadFailed"),
					updateFailed: t("dictation.updateFailed"),
				}).then(
					() => toast(t("dictation.upgrade.ready"), undefined, "success"),
					(cause: unknown) => {
						if (cause instanceof DictationInstallCancelled) return;
						toast(t("dictation.updateFailed"), cause instanceof Error ? cause.message : undefined, "error");
					},
				);
				toast(t("dictation.upgrade.downloading", { model: modelNames[model] ?? model }), undefined, "info");
				setBusy(false);
				setOpen(false);
				return;
			}
			await change("select", model);
			if (!valid()) return;
			acknowledgeDictationChoice();
			// Whatever was chosen is now ready: get out of the way and start listening.
			setBusy(false);
			setOpen(false);
			start.current();
		} catch (cause) {
			if (!valid()) return;
			setBusy(false);
			setError(cause instanceof Error ? cause.message : t("dictation.updateFailed"));
		}
	};
	const modelIcon = (model: ModelOption) =>
		model.installed ? (
			<AudioLines className="size-6" aria-hidden="true" strokeWidth={1.6} />
		) : (
			<Download className="size-6" aria-hidden="true" strokeWidth={1.6} />
		);
	// Closing abandons a choice still waiting on the daemon, so a late reply cannot start dictation.
	const dismiss = () => {
		operation.current += 1;
		setBusy(false);
		setOpen(false);
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
