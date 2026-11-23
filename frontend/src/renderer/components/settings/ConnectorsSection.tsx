import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Globe, Loader2, LogOut, MoreHorizontal, Pencil, Plus, RefreshCw, Terminal, Trash2 } from "@/components/app-icons";
import { connectorsApi, connectorsQueryKey, useConnectors, type Connector } from "../../hooks/useConnectors";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "../ui/dropdown-menu";
import { Switch } from "../ui/switch";
import { ConnectorDialog } from "./ConnectorDialog";
import { ConnectorSignInDialog } from "./ConnectorSignInDialog";
import { SettingsRow } from "./SettingsRow";
import { SettingsSection } from "./SettingsSection";

const stepKeys = {
	reach: "settings.connectors.status.reach",
	start: "settings.connectors.status.start",
	"sign-in": "settings.connectors.status.signIn",
	initialize: "settings.connectors.status.initialize",
	"list tools": "settings.connectors.status.listTools",
} as const;

export function connectorTarget(connector: Pick<Connector, "transport" | "url" | "command" | "args">): string {
	if (connector.transport === "http") return (connector.url ?? "").replace(/^https?:\/\//, "");
	return [connector.command, ...connector.args].filter(Boolean).join(" ");
}

export function needsSignIn(connector: Pick<Connector, "auth" | "signedIn" | "status" | "transport">): boolean {
	return connector.transport === "http" && connector.auth === "oauth" && (connector.status === "needs_signin" || (!connector.signedIn && connector.status !== "connected"));
}

export function ConnectorsSection({ titleHidden }: { titleHidden?: boolean }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const query = useConnectors();
	const [editing, setEditing] = useState<Connector | "new" | null>(null);
	const [signingIn, setSigningIn] = useState<Connector | null>(null);
	const [removing, setRemoving] = useState<Connector | null>(null);
	const refresh = () => queryClient.invalidateQueries({ queryKey: connectorsQueryKey });

	const act = useMutation({
		mutationFn: async (action: { kind: "enable"; connector: Connector; enabled: boolean } | { kind: "test" | "signOut" | "remove"; connector: Connector }) => {
			const id = action.connector.id;
			if (action.kind === "enable") await connectorsApi.update(id, { enabled: action.enabled });
			else if (action.kind === "test") await connectorsApi.test(id);
			else if (action.kind === "signOut") await connectorsApi.signOut(id);
			else await connectorsApi.remove(id);
		},
		onSuccess: (_data, action) => {
			if (action.kind === "remove") setRemoving(null);
		},
		onSettled: refresh,
	});
	const busyId = act.isPending ? act.variables.connector.id : null;
	const connectors = query.data ?? [];
	const statusLine = (connector: Connector) => {
		if (!connector.enabled) return t("settings.connectors.status.off");
		if (needsSignIn(connector)) return t("settings.connectors.status.needsSignIn");
		if (connector.status === "needs_signin") return t("settings.connectors.status.signIn");
		if (connector.status === "connected") {
			return `${t("settings.connectors.status.connected")} · ${t("settings.connectors.status.tools", { count: connector.tools.length })}`;
		}
		if (connector.status === "unavailable") {
			const step = connector.statusDetail?.step as keyof typeof stepKeys | undefined;
			return step && stepKeys[step] ? t(stepKeys[step]) : t("settings.connectors.status.unavailable");
		}
		return t("settings.connectors.status.unchecked");
	};

	return (
		<>
			<SettingsSection
				title={t("settings.connectors")}
				titleHidden={titleHidden}
				sectionId="connectors"
				grouped
				action={(
					<Button type="button" variant="ghost" size="sm" onClick={() => setEditing("new")}>
						<Plus aria-hidden="true" />
						{t("settings.connectors.add")}
					</Button>
				)}
			>
				{query.isLoading ? (
					<div className="settings-row-bar text-caption text-settings-muted">
						<span className="flex items-center gap-2"><Loader2 className="size-3 animate-spin" aria-hidden="true" /> {t("settings.connectors.loading")}</span>
					</div>
				) : query.error && query.data === undefined ? (
					<div className="settings-row-bar text-caption text-error" role="alert">{query.error.message}</div>
				) : connectors.length === 0 ? (
					<div className="settings-row-bar text-caption text-settings-muted">{t("settings.connectors.empty")}</div>
				) : (
					<div role="list" className="divide-y divide-border">
						{connectors.map((connector) => {
							const failed = connector.enabled && connector.status === "unavailable";
							const tone = failed ? "text-error" : connector.enabled && (needsSignIn(connector) || connector.status === "needs_signin") ? "text-warning" : undefined;
							return (
								<SettingsRow
									key={connector.id}
									role="listitem"
									data-connector={connector.key}
									icon={connector.transport === "http" ? Globe : Terminal}
									label={connector.name}
									description={(
										<>
											<span className="break-all font-mono text-xs">{connectorTarget(connector)}</span>
											<span className={tone} title={failed ? connector.statusDetail?.error : undefined}>{` · ${statusLine(connector)}`}</span>
										</>
									)}
								>
									<div className="flex items-center gap-2">
									{needsSignIn(connector) && connector.enabled ? (
										<Button type="button" variant="secondary" size="sm" className="mr-1.5" onClick={() => setSigningIn(connector)}>
											{t("settings.connectors.signIn")}
										</Button>
									) : null}
									<Switch
										aria-label={t("settings.connectors.enabledAria", { name: connector.name })}
										checked={connector.enabled}
										disabled={busyId === connector.id}
										onCheckedChange={(enabled) => act.mutate({ kind: "enable", connector, enabled })}
									/>
									<DropdownMenu>
										<DropdownMenuTrigger asChild>
											<Button type="button" variant="quiet" size="icon" className="shrink-0" aria-label={t("settings.connectors.actions", { name: connector.name })}>
												{busyId === connector.id ? <Loader2 className="size-4 animate-spin" aria-hidden="true" /> : <MoreHorizontal className="size-4" aria-hidden="true" />}
											</Button>
										</DropdownMenuTrigger>
										<DropdownMenuContent align="end" className="min-w-40">
											<DropdownMenuItem onSelect={() => act.mutate({ kind: "test", connector })}>
												<RefreshCw aria-hidden="true" />
												{t("settings.connectors.test")}
											</DropdownMenuItem>
											<DropdownMenuItem onSelect={() => setEditing(connector)}>
												<Pencil aria-hidden="true" />
												{t("settings.connectors.edit")}
											</DropdownMenuItem>
											{connector.signedIn ? (
												<DropdownMenuItem onSelect={() => act.mutate({ kind: "signOut", connector })}>
													<LogOut aria-hidden="true" />
													{t("settings.connectors.signOut")}
												</DropdownMenuItem>
											) : null}
											<DropdownMenuSeparator />
											<DropdownMenuItem
												className="text-destructive focus:text-destructive [&_svg]:text-destructive focus:[&_svg]:text-destructive"
												onSelect={() => setRemoving(connector)}
											>
												<Trash2 aria-hidden="true" />
												{t("settings.connectors.remove")}
											</DropdownMenuItem>
										</DropdownMenuContent>
									</DropdownMenu>
									</div>
								</SettingsRow>
							);
						})}
					</div>
				)}
				{act.error && !removing ? <div className="settings-row-bar text-caption text-error" role="alert">{act.error.message}</div> : null}
			</SettingsSection>
			<p className="px-(--settings-row-inset) text-control leading-4 text-settings-muted">{t("settings.connectors.note")}</p>
			{editing ? (
				<ConnectorDialog
					connector={editing === "new" ? undefined : editing}
					taken={connectors.flatMap((connector) => [connector.key, connector.presetId ?? ""])}
					onClose={() => setEditing(null)}
					onSaved={(saved) => {
						setEditing(null);
						void refresh();
						const first = saved[0];
						if (saved.length === 1 && first && needsSignIn(first)) setSigningIn(first);
						else void Promise.allSettled(saved.map((connector) => connectorsApi.test(connector.id))).then(refresh);
					}}
				/>
			) : null}
			{signingIn ? (
				<ConnectorSignInDialog
					connector={connectors.find((connector) => connector.id === signingIn.id) ?? signingIn}
					onClose={() => {
						setSigningIn(null);
						void refresh();
					}}
				/>
			) : null}
			<ConfirmDialog
				open={removing !== null}
				title={t("settings.connectors.removeTitle", { name: removing?.name ?? "" })}
				description={t("settings.connectors.removeBody")}
				confirmLabel={t("settings.connectors.remove")}
				destructive
				busy={act.isPending}
				error={removing && act.error ? act.error.message : null}
				onConfirm={() => {
					if (removing) act.mutate({ kind: "remove", connector: removing });
				}}
				onOpenChange={(open) => {
					if (!open) setRemoving(null);
				}}
			/>
		</>
	);
}
