import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { connectorsApi, connectorsQueryKey, sessionProposalsQueryKey, type Connector, type ConnectorProposal } from "../../hooks/useConnectors";
import { ConnectorSignInDialog } from "../settings/ConnectorSignInDialog";
import { connectorTarget, needsSignIn } from "../settings/ConnectorsSection";
import type { ReadyConnector } from "../../stores/connector-restart-store";
import { SettingsOptionMenu } from "../settings/SettingsOptionMenu";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

const ALL_PROJECTS = "";

const authKeys = {
	oauth: "settings.connectors.dialog.authOAuth",
	header: "settings.connectors.dialog.authHeader",
	none: "settings.connectors.dialog.authNone",
} as const;

export type ConnectorRestart = { ready?: string; busy: boolean; offer: (connector?: ReadyConnector) => void; restart: () => Promise<unknown> };

export function ConnectorProposalDock({ sessionId, hostId, asked, waiting, restart }: { sessionId: string; hostId?: string; asked: boolean; waiting: boolean; restart?: ConnectorRestart }) {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const [draft, setDraft] = useState<{ id?: string; values: Record<string, string>; scope?: string; reviewed?: boolean }>({ values: {} });
	const [signIn, setSignIn] = useState<Connector | null>(null);
	const query = useQuery({
		queryKey: sessionProposalsQueryKey(sessionId, hostId),
		queryFn: () => connectorsApi.sessionProposals(sessionId, hostId),
		enabled: asked,
		refetchInterval: (state) => (waiting || (state.state.data?.length ?? 0) > 0 ? 2500 : false),
	});
	const proposal: ConnectorProposal | undefined = query.data?.[0];
	const values = draft.id === proposal?.id ? draft.values : {};
	const scope = draft.id === proposal?.id ? draft.scope : undefined;
	const reviewed = proposal?.transport !== "stdio" || (draft.id === proposal.id && draft.reviewed === true);
	const settle = () => {
		setDraft({ values: {} });
		void queryClient.invalidateQueries({ queryKey: sessionProposalsQueryKey(sessionId, hostId) });
	};
	const approve = useMutation({
		mutationFn: (pending: ConnectorProposal) => connectorsApi.approveProposal(pending.id, {
			projectId: scope ?? pending.projectId ?? ALL_PROJECTS,
			values,
		}, hostId),
		onSuccess: (connector) => {
			settle();
			void queryClient.invalidateQueries({ queryKey: connectorsQueryKey });
			if (needsSignIn(connector)) setSignIn(connector);
			else restart?.offer({ key: connector.key, name: connector.name });
		},
	});
	const restarting = useMutation({ mutationFn: async () => restart?.restart(), onSuccess: () => restart?.offer() });
	const decline = useMutation({ mutationFn: (pending: ConnectorProposal) => connectorsApi.declineProposal(pending.id, hostId), onSuccess: settle });
	const signInDialog = signIn ? (
		<ConnectorSignInDialog
			connector={signIn}
			hostId={hostId}
			onClose={(signedIn) => {
				setSignIn(null);
				if (signedIn) restart?.offer({ key: signIn.key, name: signIn.name });
			}}
		/>
	) : null;
	if (!proposal) {
		return (
			<>
				{restart?.ready ? (
					<div
						role="group"
						aria-label={t("chat.connectorRestart.dock")}
						data-connector-restart
						className="animate-in fade-in-0 slide-in-from-bottom-1 duration-200 mb-2 flex flex-wrap items-center justify-between gap-3 rounded-[var(--radius-chat-composer)] border border-border-strong bg-surface p-3 text-sm shadow-sm"
					>
						<div className="flex min-w-0 flex-col gap-0.5">
							<p className="font-medium text-foreground">{t("chat.connectorRestart.title", { name: restart.ready })}</p>
							<p className="text-xs leading-snug text-muted-foreground">{t(restart.busy && !restarting.isPending ? "chat.connectorRestart.busy" : "chat.connectorRestart.body")}</p>
							{restarting.error ? <p role="alert" className="text-xs leading-snug text-destructive">{restarting.error.message}</p> : null}
						</div>
						<div className="flex items-center gap-2">
							<Button type="button" variant="secondary" size="sm" disabled={restarting.isPending} onClick={() => restart.offer()}>
								{t("chat.connectorRestart.later")}
							</Button>
							<Button type="button" variant="accent" size="sm" disabled={restart.busy || restarting.isPending} onClick={() => restarting.mutate()}>
								{t(restarting.isPending ? "chat.connectorRestart.restarting" : "chat.connectorRestart.restart")}
							</Button>
						</div>
					</div>
				) : null}
				{signInDialog}
			</>
		);
	}

	const needed = [...proposal.envKeys, ...proposal.headerKeys];
	const busy = approve.isPending || decline.isPending;
	const error = approve.error ?? decline.error;
	const projectId = proposal.projectId ?? ALL_PROJECTS;
	return (
		<>
			<div
				role="group"
				aria-label={t("chat.connectorProposal.dock")}
				data-connector-proposal={proposal.key}
				className="animate-in fade-in-0 slide-in-from-bottom-1 duration-200 mb-2 flex flex-col gap-3 rounded-[var(--radius-chat-composer)] border border-border-strong bg-surface p-3 text-sm shadow-sm"
			>
				<div className="flex flex-col gap-0.5">
					<p className="font-medium text-foreground">{t("chat.connectorProposal.title")}</p>
					<p className="whitespace-pre-wrap break-words text-xs leading-snug text-muted-foreground">{proposal.reason || t("chat.connectorProposal.asked")}</p>
				</div>
				{proposal.transport === "stdio" ? (
					<div className="flex flex-col gap-2">
						<p className="text-foreground">{proposal.key}</p>
						{reviewed ? (
							<dl className="flex flex-col gap-2 text-xs">
								<div className="flex flex-col gap-0.5">
									<dt className="text-muted-foreground">{t("settings.connectors.dialog.command")}</dt>
									<dd className="break-all font-mono text-foreground">{proposal.command}</dd>
								</div>
								<div className="flex flex-col gap-0.5">
									<dt className="text-muted-foreground">{t("chat.connectorProposal.arguments")}</dt>
									<dd className="break-all font-mono text-foreground">{JSON.stringify(proposal.args)}</dd>
								</div>
							</dl>
						) : (
							<p className="text-xs text-muted-foreground">{t("settings.connectors.dialog.tabCommand")}</p>
						)}
					</div>
				) : (
					<div className="flex flex-col gap-0.5">
						<p className="text-foreground">{proposal.key}</p>
						<p className="break-all font-mono text-xs text-muted-foreground">{connectorTarget(proposal)}</p>
					</div>
				)}
				{(reviewed ? needed : []).map((name) => (
					<Input
						key={name}
						type="password"
						autoComplete="off"
						className="font-mono text-xs"
						aria-label={t("settings.connectors.dialog.importValue", { name })}
						placeholder={t("settings.connectors.dialog.importValue", { name })}
						value={values[name] ?? ""}
						disabled={busy}
						onChange={(event) => setDraft({ id: proposal.id, values: { ...values, [name]: event.target.value }, scope, reviewed })}
					/>
				))}
				<div className="flex flex-wrap items-center justify-between gap-2">
					<div className="flex min-w-0 items-center gap-2 text-xs text-muted-foreground">
						{proposal.transport === "http" ? <span>{`${t("settings.connectors.dialog.signIn")}: ${t(authKeys[proposal.auth])}`}</span> : null}
						{projectId ? (
							<SettingsOptionMenu<string>
								aria-label={t("settings.connectors.dialog.projects")}
								value={scope ?? projectId}
								onChange={(next) => setDraft({ id: proposal.id, values, scope: next, reviewed })}
								disabled={busy}
								triggerVariant="ghost"
								menuAlign="start"
								options={[
									{ value: projectId, label: t("chat.connectorProposal.thisProject") },
									{ value: ALL_PROJECTS, label: t("settings.connectors.dialog.allProjects") },
								]}
							/>
						) : null}
					</div>
					<div className="flex items-center gap-2">
						<Button type="button" variant="secondary" size="sm" disabled={busy} onClick={() => decline.mutate(proposal)}>
							{t("chat.connectorProposal.decline")}
						</Button>
						{reviewed ? (
							<Button type="button" variant="accent" size="sm" disabled={busy || needed.some((name) => !values[name])} onClick={() => approve.mutate(proposal)}>
								{t("chat.connectorProposal.approve")}
							</Button>
						) : (
							<Button type="button" variant="accent" size="sm" disabled={busy} onClick={() => setDraft({ id: proposal.id, values, scope, reviewed: true })}>
								{t("chat.connectorProposal.review")}
							</Button>
						)}
					</div>
				</div>
				{proposal.transport === "stdio" && reviewed ? <p className="text-xs leading-snug text-muted-foreground">{t("chat.connectorProposal.runs")}</p> : null}
				{error ? <p role="alert" className="text-xs leading-snug text-destructive">{error.message}</p> : null}
			</div>
			{signInDialog}
		</>
	);
}
