import { useId, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { AgentAccount, AgentAccountColor } from "../../hooks/useAgentAccounts";
import { ConfirmDialog } from "../ConfirmDialog";
import { Input } from "../ui/input";
import { AccountColorPicker } from "./AccountColorPicker";

/** Renames an account and sets its color, one no other account of the provider uses. A failed save
 * stays in the dialog. */
export function AccountEditDialog({ account, label, takenColors, onSave, onClose }: {
	account: AgentAccount;
	label: string;
	/** The colors the provider's other accounts use, each with the account it belongs to. */
	takenColors: ReadonlyMap<AgentAccountColor, string>;
	onSave: (change: { name: string; color: AgentAccountColor | "" }) => Promise<unknown>;
	onClose: () => void;
}) {
	const { t } = useTranslation();
	const id = useId();
	const nameRef = useRef<HTMLInputElement>(null);
	const [name, setName] = useState(account.name ?? "");
	const [color, setColor] = useState<AgentAccountColor | undefined>(account.color);
	const [pending, setPending] = useState(false);
	const [error, setError] = useState("");
	const save = async () => {
		setPending(true);
		setError("");
		try {
			await onSave({ name: name.trim(), color: color ?? "" });
			onClose();
		} catch (failure) {
			setError(failure instanceof Error ? failure.message : t("settings.agentAccounts.editFailed"));
			setPending(false);
		}
	};
	return <ConfirmDialog open
		title={t("settings.agentAccounts.editTitle")}
		description={label}
		confirmLabel={t(pending ? "settings.agentAccounts.saving" : "settings.agentAccounts.save")}
		busy={pending}
		error={error || undefined}
		initialFocusRef={nameRef}
		onConfirm={() => void save()}
		onOpenChange={(open) => { if (!open) onClose(); }}
	>
		<div className="space-y-3">
			<div className="space-y-1.5">
				<label htmlFor={`${id}-name`} className="text-xs text-muted-foreground">{t("settings.agentAccounts.apiKeyName")}</label>
				<Input ref={nameRef} id={`${id}-name`} value={name} maxLength={80} autoComplete="off" disabled={pending} placeholder={t("settings.agentAccounts.apiKeyNamePlaceholder")} onChange={(event) => setName(event.target.value)} onKeyDown={(event) => { if (event.key !== "Enter" || event.nativeEvent.isComposing || event.keyCode === 229) return; event.preventDefault(); void save(); }} />
			</div>
			<div className="space-y-1.5">
				<span id={`${id}-color`} className="block text-xs text-muted-foreground">{t("settings.agentAccounts.colorLabel")}</span>
				<AccountColorPicker value={color} onChange={setColor} taken={takenColors} disabled={pending} labelledBy={`${id}-color`} />
			</div>
		</div>
	</ConfirmDialog>;
}
