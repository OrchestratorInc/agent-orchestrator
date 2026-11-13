import { Input } from "../ui/input";
import { useTranslation } from "react-i18next";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { apiClient } from "../../lib/api-client";
import { dictationClient } from "../../lib/dictation-client";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";
import { SettingsRow } from "./SettingsRow";
import { useDictationSounds } from "../../lib/dictation-sound";
import { SettingsSection } from "./SettingsSection";
import { useDictation } from "../chat/useDictation";
import { DictationMeter } from "../chat/DictationMeter";
import { DictationProgress } from "../chat/DictationProgress";

export function DictationSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const cache = useQueryClient();
	const [search, setSearch] = useState("");
	const [sounds, setSounds] = useDictationSounds();
	const [error, setError] = useState<string>();
	const [pending, setPending] = useState(false);
	const [authorizing, setAuthorizing] = useState(false);
	const [transcript, setTranscript] = useState("");
	const test = useDictation({
		ownerKey: "dictation-settings-test",
		onText: setTranscript,
		onEnd: (keep) => {
			if (!keep) setTranscript("");
		},
	});
	const status = useQuery({
		queryKey: ["dictation"],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/dictation");
			if (error || !data) throw new Error(t("dictation.loadFailed"));
			return data;
		},
		refetchInterval: 1500,
	});
	const change = async (action: "install" | "select" | "delete" | "cancel", model: string) => {
		setPending(true);
		setError(undefined);
		try {
			if (action === "select" && model === "native-macos") {
				// The macOS prompts (speech recognition, microphone) are settled once, here, when the user
				// turns macOS dictation on. If access was already granted this returns at once.
				setAuthorizing(true);
				const access = await dictationClient.authorize().finally(() => setAuthorizing(false));
				if (!access.granted) throw new Error(access.message ?? t("dictation.updateFailed"));
			}
			const { data, error } = await apiClient.POST("/api/v1/dictation", { body: { action, model } });
			if (error || !data) throw new Error(error?.message ?? t("dictation.updateFailed"));
			cache.setQueryData(["dictation"], data);
		} catch (error) {
			setError(error instanceof Error ? error.message : t("dictation.updateFailed"));
		} finally {
			setPending(false);
		}
	};
	const busy = pending || Boolean(status.data?.installing) || Boolean(status.data?.recording) || test.phase !== "idle";
	return (
		<SettingsSection title={t("settings.dictation")} titleHidden={titleHidden} sectionId="dictation" grouped>
			<SettingsRow label={t("dictation.sounds")} description={t("dictation.soundsDescription")}>
				<Switch aria-label={t("dictation.sounds")} checked={sounds} onCheckedChange={setSounds} />
			</SettingsRow>
			{status.isPending && <div className="settings-content-row">{t("dictation.loading")}</div>}
			<div className="settings-content-row">
				<Input
					value={search}
					onChange={(event) => setSearch(event.target.value)}
					placeholder={t("dictation.searchModels")}
					aria-label={t("dictation.searchModels")}
				/>
			</div>
			{status.data?.models
				.filter((model) =>
					`${model.name} ${model.description ?? ""} ${model.languages?.join(" ") ?? ""}`
						.toLowerCase()
						.includes(search.toLowerCase()),
				)
				.map((model) => (
					<div key={model.id} className="settings-content-row flex items-center justify-between gap-4">
						<div className="min-w-0">
							<div className="text-sm font-medium">
								{model.id.startsWith("handy-computer/") ? (
									<a
										href={`https://huggingface.co/${model.id}`}
										target="_blank"
										rel="noreferrer"
										className="hover:underline"
										title={model.license}
									>
										{model.name}
									</a>
								) : (
									model.name
								)}
							</div>
							{model.id.startsWith("phonon-") && (
								<a
									href="https://www.fermionresearch.com/models/phonon-2-5/"
									target="_blank"
									rel="noreferrer"
									className="text-xs text-muted-foreground hover:underline"
								>
									{t("dictation.attribution")}
								</a>
							)}
							{model.description && <p className="text-xs text-muted-foreground">{model.description}</p>}
							<div className="text-xs text-muted-foreground">
								{model.id === "native-macos"
									? t("dictation.nativeDetail")
									: t("dictation.modelDetail", {
											size: Math.round(model.downloadBytes / 1_000_000),
											status: model.installed ? t("dictation.installed") : t("dictation.notInstalled"),
										})}
							</div>
						</div>
						<div className="flex shrink-0 gap-2">
							{model.installed ? (
								<>
									<Button
										size="sm"
										variant="outline"
										disabled={busy || model.id === status.data?.selected}
										onClick={() => void change("select", model.id)}
									>
										{model.id === status.data?.selected ? t("dictation.selected") : t("dictation.use")}
									</Button>
									{model.id !== "native-macos" && (
										<Button
											size="sm"
											variant="ghost"
											disabled={busy || model.id === status.data?.selected}
											onClick={() => void change("delete", model.id)}
										>
											{t("dictation.delete")}
										</Button>
									)}
								</>
							) : (
								<Button
									size="sm"
									variant="outline"
									disabled={busy || !model.available}
									onClick={() => void change("install", model.id)}
								>
									{model.available ? t("dictation.download") : t("dictation.unavailable")}
								</Button>
							)}
						</div>
					</div>
				))}
			{authorizing && (
				<div className="settings-content-row text-sm text-settings-muted" role="status">
					{t("dictation.upgrade.allowAccess")}
				</div>
			)}
			{status.data?.installing && (
				<div className="settings-content-row flex items-center justify-between gap-6">
					<div className="min-w-0 flex-1">
						<DictationProgress
							stage={status.data.stage}
							done={status.data.progressDone}
							total={status.data.progressTotal}
						/>
					</div>
					<Button size="sm" variant="ghost" disabled={pending} onClick={() => void change("cancel", "")}>
						{t("dictation.cancel")}
					</Button>
				</div>
			)}
			{(error || status.data?.error || status.error || test.error) && (
				<div className="settings-content-row text-sm text-destructive" role="alert">
					{error || test.error || status.data?.error || status.error?.message}
				</div>
			)}
			<div className="settings-content-row flex flex-col gap-3">
				<div className="flex items-center gap-3">
					<Button
						variant="outline"
						size="sm"
						disabled={
							test.phase === "idle" &&
							(busy || !status.data?.models.some((model) => model.id === status.data?.selected && model.installed))
						}
						// The selected model is already usable here (the button is disabled otherwise), so this never
						// reopens the first-use chooser, which could replace the user's selection.
						onClick={() => {
							if (test.phase === "idle") setTranscript("");
							test.toggle();
						}}
					>
						{test.phase === "idle"
							? t("dictation.test")
							: test.phase === "starting"
								? t("dictation.starting")
								: test.phase === "finishing"
									? t("dictation.finishing")
									: t("dictation.finishTest")}
					</Button>
					{test.phase === "listening" && <DictationMeter listening />}
				</div>
				{(transcript || test.phase !== "idle") && (
					<p className="text-sm text-muted-foreground" aria-live="polite">
						{transcript || t("dictation.testHint")}
					</p>
				)}
			</div>
		</SettingsSection>
	);
}
