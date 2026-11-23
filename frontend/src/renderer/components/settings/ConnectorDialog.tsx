import { useMutation, useQuery } from "@tanstack/react-query";
import { useId, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import { connectorImportQueryKey, connectorKeyFrom, connectorsApi, joinCommandLine, splitCommandLine, type Connector, type ConnectorInput } from "../../hooks/useConnectors";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { Button } from "../ui/button";
import { Checkbox } from "../ui/checkbox";
import { CloseButton } from "../ui/close-button";
import {
	Dialog,
	DialogClose,
	DialogContent,
	DialogTitle,
	settingsDialogBodyClass,
	settingsDialogContentClass,
	settingsDialogFooterClass,
	settingsDialogHeaderClass,
} from "../ui/dialog";
import { Input } from "../ui/input";
import { Label } from "../ui/label";
import { Tabs, TabsList, TabsTrigger } from "../ui/tabs";
import { connectorPresets, type ConnectorPreset } from "./connectorCatalog";
import { connectorTarget } from "./ConnectorsSection";
import { SettingsOptionMenu } from "./SettingsOptionMenu";

type Mode = "url" | "command" | "import";
type Auth = "oauth" | "header" | "none";
const ALL_PROJECTS = "";

export function parseEnvLines(text: string): Record<string, string> | null {
	const out: Record<string, string> = {};
	for (const line of text.split("\n")) {
		if (!line.trim()) continue;
		const at = line.indexOf("=");
		const name = (at < 0 ? line : line.slice(0, at)).trim();
		if (!name || at < 0) return null;
		out[name] = line.slice(at + 1);
	}
	return out;
}

function Field({ id, label, hint, children }: { id: string; label: string; hint?: string; children: React.ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<Label htmlFor={id} className="text-control font-medium text-settings-label">{label}</Label>
			{children}
			{hint ? <p className="text-caption leading-4 text-settings-muted">{hint}</p> : null}
		</div>
	);
}

export function ConnectorDialog({ connector, taken, onClose, onSaved }: {
	connector?: Connector;
	taken: string[];
	onClose: () => void;
	onSaved: (saved: Connector[]) => void;
}) {
	const { t } = useTranslation();
	const ids = useId();
	const workspaces = (useWorkspaceQuery().data ?? []).filter((workspace) => !workspace.hostId);
	const [mode, setMode] = useState<Mode>(connector?.transport === "stdio" ? "command" : "url");
	const [presetId, setPresetId] = useState(connector?.presetId ?? "");
	const [name, setName] = useState(connector?.name ?? "");
	const [key, setKey] = useState(connector?.key ?? "");
	const [keyEdited, setKeyEdited] = useState(Boolean(connector));
	const [url, setUrl] = useState(connector?.url ?? "");
	const [auth, setAuth] = useState<Auth>(connector?.auth ?? "oauth");
	const [headerName, setHeaderName] = useState(connector?.headerKeys[0] ?? "Authorization");
	const [headerValue, setHeaderValue] = useState("");
	const [command, setCommand] = useState(connector?.transport === "stdio" ? joinCommandLine([connector.command ?? "", ...connector.args]) : "");
	const [env, setEnv] = useState((connector?.envKeys ?? []).map((envKey) => `${envKey}=`).join("\n"));
	const [projectId, setProjectId] = useState(connector?.projectId ?? ALL_PROJECTS);
	const [ownApp, setOwnApp] = useState(Boolean(connector?.oauthClientId));
	const [clientId, setClientId] = useState(connector?.oauthClientId ?? "");
	const [clientSecret, setClientSecret] = useState("");
	const [chosen, setChosen] = useState<ReadonlySet<string>>(() => new Set());
	const [typed, setTyped] = useState<Record<string, Record<string, string>>>({});

	const candidates = useQuery({ queryKey: connectorImportQueryKey, queryFn: connectorsApi.importCandidates, enabled: mode === "import" });
	const envValues = parseEnvLines(env);
	const argv = splitCommandLine(command);
	const shownKey = keyEdited ? key : connectorKeyFrom(name || (mode === "url" ? url.replace(/^https?:\/\//, "").split(/[./]/).find((part) => part && part !== "mcp" && part !== "www") ?? "" : argv[argv.length - 1] ?? ""));

	const save = useMutation({
		mutationFn: async (): Promise<Connector[]> => {
			if (mode === "import") return connectorsApi.importChosen([...chosen], typed);
			const input: ConnectorInput = { key: shownKey, name: name.trim() || shownKey, projectId: projectId || null };
			if (presetId) input.presetId = presetId;
			if (mode === "url") {
				Object.assign(input, { transport: "http", url: url.trim(), auth });
				if (auth === "header") {
					const first = connector?.headerKeys[0];
					const kept = (connector?.headerKeys ?? []).filter((name) => name !== first || name === headerName.trim());
					input.headers = { ...Object.fromEntries(kept.map((name) => [name, ""])), [headerName.trim()]: headerValue };
				}
				if (auth === "oauth" && ownApp && clientId.trim()) Object.assign(input, { oauthClientId: clientId.trim(), oauthClientSecret: clientSecret });
			} else {
				Object.assign(input, { transport: "stdio", command: argv[0] ?? "", args: argv.slice(1), env: envValues ?? {} });
			}
			return [connector ? await connectorsApi.update(connector.id, input) : await connectorsApi.create(input)];
		},
		onSuccess: onSaved,
	});

	const applyPreset = (preset: ConnectorPreset) => {
		setMode("url");
		setPresetId(preset.id);
		setName(preset.name);
		setUrl(preset.url);
		setAuth(preset.auth);
		setKeyEdited(false);
	};
	const importable = (candidates.data ?? []).filter((candidate) => !candidate.exists);
	const missingValue = importable.some((candidate) => chosen.has(candidate.id) && (candidate.needsValue ?? []).some((needed) => !typed[candidate.id]?.[needed]));
	const ready = mode === "import"
		? chosen.size > 0 && !missingValue
		: Boolean(shownKey) && (mode === "url"
			? Boolean(url.trim()) && (auth !== "header" || (Boolean(headerName.trim()) && (Boolean(headerValue) || Boolean(connector?.headerKeys.includes(headerName.trim())))))
			: argv.length > 0 && envValues !== null);

	return (
		<Dialog open onOpenChange={(open) => { if (!open && !save.isPending) onClose(); }}>
			<DialogContent showCloseButton={false} className={cn(settingsDialogContentClass, "w-[min(520px,calc(100vw-24px))]")} data-connector-dialog aria-describedby={undefined}>
				<DialogClose asChild>
					<CloseButton floating aria-label={t("confirm.close")} />
				</DialogClose>
				<div className={cn(settingsDialogHeaderClass, "border-b-0 pb-0 pr-12")}>
					<DialogTitle className="settings-dialog-title text-base">
						{connector ? t("settings.connectors.dialog.editTitle", { name: connector.name }) : t("settings.connectors.dialog.addTitle")}
					</DialogTitle>
				</div>
				<form
					className="flex min-h-0 flex-col"
					onSubmit={(event) => {
						event.preventDefault();
						if (ready) save.mutate();
					}}
				>
					<div className={cn(settingsDialogBodyClass, "gap-3")}>
						{connector ? null : (
							<>
								<div role="group" aria-label={t("settings.connectors.dialog.catalog")} className="flex flex-wrap gap-1.5">
									{connectorPresets.filter((preset) => !taken.includes(preset.id) && !taken.includes(connectorKeyFrom(preset.name))).map((preset) => (
										<Button key={preset.id} type="button" variant="secondary" size="xs" shape="pill" aria-pressed={presetId === preset.id} onClick={() => applyPreset(preset)}>
											{preset.name}
										</Button>
									))}
								</div>
								<Tabs
									value={mode}
									onValueChange={(next) => {
										setMode(next as Mode);
										if (next !== "url" && presetId) {
											setPresetId("");
											setName("");
											setKeyEdited(false);
										}
									}}
								>
									<TabsList className="w-full" aria-label={t("settings.connectors.dialog.addTitle")}>
										<TabsTrigger value="url">{t("settings.connectors.dialog.tabUrl")}</TabsTrigger>
										<TabsTrigger value="command">{t("settings.connectors.dialog.tabCommand")}</TabsTrigger>
										<TabsTrigger value="import">{t("settings.connectors.dialog.tabImport")}</TabsTrigger>
									</TabsList>
								</Tabs>
							</>
						)}
						{mode === "import" ? (
							candidates.isLoading ? (
								<p className="text-caption text-settings-muted" role="status">{t("settings.connectors.dialog.importLoading")}</p>
							) : candidates.error ? (
								<p className="text-caption text-error" role="alert">{candidates.error.message}</p>
							) : (candidates.data ?? []).length === 0 ? (
								<p className="text-caption text-settings-muted">{t("settings.connectors.dialog.importEmpty")}</p>
							) : (
								<div role="list" className="settings-grouped-rows flex flex-col divide-y divide-border">
									{(candidates.data ?? []).map((candidate) => {
										const checked = chosen.has(candidate.id);
										return (
											<div key={candidate.id} role="listitem" className="flex flex-col gap-2 px-3 py-2.5">
												<label className="flex items-start gap-2.5">
													<Checkbox
														className="mt-0.5"
														checked={checked}
														disabled={candidate.exists}
														onCheckedChange={(next) => setChosen((prev) => {
															const out = new Set(prev);
															if (next === true) out.add(candidate.id);
															else out.delete(candidate.id);
															return out;
														})}
													/>
													<span className="min-w-0 flex-1">
														<span className="block text-sm leading-5 text-settings-label">{candidate.key}</span>
														<span className="block break-all text-control leading-4 text-settings-muted">
															{candidate.exists ? t("settings.connectors.dialog.importExists") : `${candidate.agent} · ${connectorTarget({ transport: candidate.transport, url: candidate.url, command: candidate.command, args: candidate.args ?? [] })}`}
														</span>
													</span>
												</label>
												{checked ? (candidate.needsValue ?? []).map((needed) => (
													<Input
														key={needed}
														type="password"
														autoComplete="off"
														className="font-mono text-xs"
														aria-label={t("settings.connectors.dialog.importValue", { name: needed })}
														placeholder={t("settings.connectors.dialog.importValue", { name: needed })}
														value={typed[candidate.id]?.[needed] ?? ""}
														onChange={(event) => setTyped((prev) => ({ ...prev, [candidate.id]: { ...prev[candidate.id], [needed]: event.target.value } }))}
													/>
												)) : null}
											</div>
										);
									})}
								</div>
							)
						) : (
							<>
								{mode === "url" ? (
									<Field id={`${ids}-url`} label={t("settings.connectors.dialog.url")}>
										<Input id={`${ids}-url`} autoFocus={!connector} className="font-mono text-xs" value={url} placeholder={t("settings.connectors.dialog.urlPlaceholder")} onChange={(event) => { setUrl(event.target.value); setPresetId(""); }} />
									</Field>
								) : (
									<Field id={`${ids}-command`} label={t("settings.connectors.dialog.command")}>
										<Input id={`${ids}-command`} autoFocus={!connector} className="font-mono text-xs" value={command} placeholder={t("settings.connectors.dialog.commandPlaceholder")} onChange={(event) => setCommand(event.target.value)} />
									</Field>
								)}
								<Field id={`${ids}-key`} label={t("settings.connectors.dialog.name")} hint={t("settings.connectors.dialog.nameHint")}>
									<Input id={`${ids}-key`} className="font-mono text-xs" maxLength={32} value={shownKey} disabled={Boolean(connector)} onChange={(event) => { setKey(event.target.value.toLowerCase().replace(/[^a-z0-9_-]/g, "").slice(0, 32)); setKeyEdited(true); }} />
								</Field>
								{mode === "url" ? (
									<>
										<div className="flex items-center justify-between gap-3">
											<span className="text-control font-medium text-settings-label">{t("settings.connectors.dialog.signIn")}</span>
											<SettingsOptionMenu<Auth>
												aria-label={t("settings.connectors.dialog.signIn")}
												value={auth}
												onChange={setAuth}
												options={[
													{ value: "oauth", label: t("settings.connectors.dialog.authOAuth") },
													{ value: "header", label: t("settings.connectors.dialog.authHeader") },
													{ value: "none", label: t("settings.connectors.dialog.authNone") },
												]}
											/>
										</div>
										{auth === "header" ? (
											<div className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-2">
												<Input aria-label={t("settings.connectors.dialog.headerName")} className="font-mono text-xs" value={headerName} onChange={(event) => setHeaderName(event.target.value)} />
												<Input
													aria-label={t("settings.connectors.dialog.headerValue")}
													type="password"
													autoComplete="off"
													className="font-mono text-xs"
													value={headerValue}
													placeholder={connector?.headerKeys.includes(headerName.trim()) ? t("settings.connectors.dialog.keepValue") : t("settings.connectors.dialog.headerValue")}
													onChange={(event) => setHeaderValue(event.target.value)}
												/>
											</div>
										) : null}
										{auth === "oauth" ? (
											<>
												<label className="flex items-center gap-2.5 text-control text-settings-label">
													<Checkbox checked={ownApp} onCheckedChange={(next) => setOwnApp(next === true)} />
													{t("settings.connectors.dialog.ownApp")}
												</label>
												{ownApp ? (
													<div className="grid grid-cols-2 gap-2">
														<Input aria-label={t("settings.connectors.dialog.clientId")} placeholder={t("settings.connectors.dialog.clientId")} className="font-mono text-xs" value={clientId} onChange={(event) => setClientId(event.target.value)} />
														<Input aria-label={t("settings.connectors.dialog.clientSecret")} placeholder={connector?.oauthClientId ? t("settings.connectors.dialog.keepValue") : t("settings.connectors.dialog.clientSecret")} type="password" autoComplete="off" className="font-mono text-xs" value={clientSecret} onChange={(event) => setClientSecret(event.target.value)} />
													</div>
												) : null}
											</>
										) : null}
									</>
								) : (
									<Field id={`${ids}-env`} label={t("settings.connectors.dialog.env")} hint={envValues === null ? undefined : t(connector?.envKeys.length ? "settings.connectors.dialog.envKeepHint" : "settings.connectors.dialog.envHint")}>
										<textarea
											id={`${ids}-env`}
											rows={3}
											spellCheck={false}
											className="w-full min-w-0 resize-y rounded-md border border-transparent bg-input/50 px-3 py-2 font-mono text-xs text-foreground outline-none placeholder:text-muted-foreground"
											value={env}
											onChange={(event) => setEnv(event.target.value)}
										/>
										{envValues === null ? <p className="text-caption leading-4 text-error" role="alert">{t("settings.connectors.dialog.invalidEnv")}</p> : null}
									</Field>
								)}
								<div className="flex items-center justify-between gap-3">
									<span className="text-control font-medium text-settings-label">{t("settings.connectors.dialog.projects")}</span>
									<SettingsOptionMenu<string>
										aria-label={t("settings.connectors.dialog.projects")}
										value={projectId}
										onChange={setProjectId}
										options={[{ value: ALL_PROJECTS, label: t("settings.connectors.dialog.allProjects") }, ...workspaces.map((workspace) => ({ value: workspace.id, label: workspace.name }))]}
									/>
								</div>
							</>
						)}
						{save.error ? <p role="alert" className="text-caption leading-4 text-error">{save.error.message}</p> : null}
					</div>
					<div className={cn(settingsDialogFooterClass, "border-t-0 pt-0")}>
						<Button type="button" variant="secondary" disabled={save.isPending} onClick={onClose}>{t("confirm.cancel")}</Button>
						<Button type="submit" variant="primary" disabled={!ready || save.isPending}>
							{mode === "import" ? t("settings.connectors.dialog.importAction", { count: chosen.size }) : connector ? t("settings.connectors.dialog.save") : t("settings.connectors.add")}
						</Button>
					</div>
				</form>
			</DialogContent>
		</Dialog>
	);
}
