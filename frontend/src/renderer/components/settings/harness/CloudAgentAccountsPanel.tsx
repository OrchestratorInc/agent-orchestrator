import { Plus, Trash2 } from "@/components/app-icons";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { cloudAgentAccountName, isCloudSubscriptionAccount, useCloudAgentAccountActions, useCloudAgentAccounts } from "../../../hooks/useCloudAgentAccounts";
import type { CloudCpAgentAccount } from "../../../lib/cloud-cp";
import { ConfirmDialog } from "../../ConfirmDialog";
import { Button } from "../../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../../ui/tooltip";
import { formatPlanName } from "../AgentAccountsSection";
import { CloudHarnessLoginPanel, type CloudHarness } from "../CloudHarnessLoginPanel";
import { HarnessDetailGroup } from "./harness-ui";

/** One harness's Cloud accounts, as the local Account tab lists a CLI's: each
 * login or key, the default one new tasks run on, signing one in again, and
 * adding or removing one. A session moves between them in its Summary. */
export function CloudAgentAccountsPanel({ agent, addFirst = false }: { agent: CloudHarness; addFirst?: boolean }) {
	const { t } = useTranslation();
	const accounts = useCloudAgentAccounts();
	const actions = useCloudAgentAccountActions();
	// "new" adds an account; an account id signs that account in again.
	const [login, setLogin] = useState<string | null>(addFirst ? "new" : null);
	const [busy, setBusy] = useState<string | null>(null);
	const [error, setError] = useState("");
	const [removing, setRemoving] = useState<CloudCpAgentAccount | null>(null);
	const rows = (accounts.data ?? []).filter((account) => account.agent === agent);

	const run = async (key: string, action: () => Promise<unknown>) => {
		setBusy(key);
		setError("");
		try {
			await action();
		} catch (err) {
			setError(err instanceof Error ? err.message : String(err));
		} finally {
			setBusy(null);
		}
	};
	const loginPanel = (id: string) => login === id
		? <div className="px-3 py-2.5"><CloudHarnessLoginPanel agent={agent} account={{ id, credentialType: rows.find((row) => row.id === id)?.credentialType }} onClose={() => setLogin(null)} /></div>
		: null;

	return <>
		<HarnessDetailGroup
			title={t("settings.harness.account")}
			action={<Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="secondary" aria-label={t("settings.codexAccounts.add")} disabled={busy !== null || login !== null} onClick={() => { setError(""); setLogin("new"); }}><Plus aria-hidden="true" /></Button></TooltipTrigger><TooltipContent>{t("settings.codexAccounts.add")}</TooltipContent></Tooltip>}
		>
			{error ? <p role="alert" className="px-3 py-2.5 text-xs text-error">{error}</p> : null}
			{loginPanel("new")}
			{accounts.isError ? <p role="alert" className="px-3 py-2.5 text-xs text-error">{accounts.error.message}</p> : null}
			{accounts.isLoading ? <p className="px-3 py-2.5 text-xs text-muted-foreground">{t("settings.agentAccounts.loading")}</p> : null}
			{rows.map((account) => <div key={account.id}>
				<CloudAccountRow
					account={account}
					busy={busy !== null || login !== null}
					onMakeDefault={() => void run(`default:${account.id}`, () => actions.makeDefault(account.id))}
					onSignInAgain={() => { setError(""); setLogin(account.id); }}
					onRemove={() => { setError(""); setRemoving(account); }}
				/>
				{loginPanel(account.id)}
			</div>)}
		</HarnessDetailGroup>
		{removing ? <ConfirmDialog open destructive
			title={t("settings.agentAccounts.remove")}
			description={t("settings.agentAccounts.cloudRemoveDescription", { email: cloudAgentAccountName(removing, t) })}
			confirmLabel={t(busy === `remove:${removing.id}` ? "settings.agentAccounts.removing" : "settings.agentAccounts.remove")}
			busy={busy === `remove:${removing.id}`}
			error={error || undefined}
			onConfirm={() => void run(`remove:${removing.id}`, async () => { await actions.remove(removing.id); setRemoving(null); })}
			onOpenChange={(open) => { if (!open) setRemoving(null); }}
		/> : null}
	</>;
}

function CloudAccountRow({ account, busy, onMakeDefault, onSignInAgain, onRemove }: {
	account: CloudCpAgentAccount;
	busy: boolean;
	onMakeDefault: () => void;
	onSignInAgain: () => void;
	onRemove: () => void;
}) {
	const { t } = useTranslation();
	const plan = formatPlanName(account.plan);
	const invalid = account.validationState === "invalid";
	const meta = [
		plan ? t("settings.codexAccounts.planLabel", { name: plan }) : null,
		// A name the user gave hides the login's email; keep it findable.
		account.name && account.email ? account.email : null,
		invalid ? t("settings.agentAccounts.signedOut") : isCloudSubscriptionAccount(account) ? null : t("settings.agentAccounts.noSubscription"),
	].filter(Boolean).join(" · ");
	return <div className="px-3 py-2.5" data-agent-account={account.id}>
		<div className="flex items-start justify-between gap-3">
			<div className="min-w-0">
				<p className="truncate text-sm font-medium text-foreground">{cloudAgentAccountName(account, t)}</p>
				{meta ? <p className="mt-0.5 truncate text-xs text-muted-foreground">{meta}</p> : null}
			</div>
			<div className="flex shrink-0 items-center gap-2">
				{invalid ? <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={onSignInAgain}>{t("settings.agentAccounts.signInAgain")}</Button> : null}
				{account.isDefault
					? <span className="text-xs text-muted-foreground">{t("settings.agentAccounts.default")}</span>
					: invalid ? null : <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={onMakeDefault}>{t("settings.agentAccounts.makeDefault")}</Button>}
				<Tooltip><TooltipTrigger asChild><Button type="button" size="icon-sm" variant="destructive-ghost" aria-label={t("settings.agentAccounts.remove")} disabled={busy} onClick={onRemove}><Trash2 aria-hidden="true" /></Button></TooltipTrigger><TooltipContent>{t("settings.agentAccounts.remove")}</TooltipContent></Tooltip>
			</div>
		</div>
		{invalid && account.invalidReason ? <p className="mt-2 text-xs text-warning">{account.invalidReason}</p> : null}
	</div>;
}
