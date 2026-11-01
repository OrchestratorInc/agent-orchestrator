import { useState } from "react";
import { AccessibilityInfo } from "react-native";
import type { ServerConfig } from "./config";
import { portError } from "./connectionFields";
import { canSaveDraft, isDraftDirty, MachineSaveError, machineEditDraft, type EnvironmentMachineDraft } from "./environmentEditor";
import { haptics } from "./haptics";
import type { Host } from "./hosts";
import type { MachineSymbol } from "./machineAppearance";

export type MachineEditorFailure = { message: string; field: "host" | "password" };

/**
 * The machine editor's draft and its Save. The draft starts from the machine
 * as saved and edits the route the phone is connected over (or the first one).
 * `onSave` persists it; a rejection stays on the page as an error against the
 * field it is about.
 */
export function useMachineEditor(host: Host, activeConfig: ServerConfig | undefined, onSave: (draft: EnvironmentMachineDraft) => Promise<void>) {
	const [initial] = useState(() => machineEditDraft(host, activeConfig));
	const [draft, setDraft] = useState(initial);
	const [saving, setSaving] = useState(false);
	const [failure, setFailure] = useState<MachineEditorFailure | null>(null);

	// Editing clears the error shown against the form; the next save says again.
	const edit = (change: (current: EnvironmentMachineDraft) => EnvironmentMachineDraft) => {
		setDraft(change);
		setFailure(null);
	};
	const setConfig = (key: "host" | "httpPort" | "password") => (value: string) =>
		edit((current) => ({ ...current, config: { ...current.config, [key]: value } }));

	const dirty = isDraftDirty(initial, draft);
	// Save waits for a change, as a form's Done does.
	const canSave = dirty && canSaveDraft(draft) && !saving;

	/** Resolves true once the draft is saved. */
	async function save(): Promise<boolean> {
		if (!canSave) return false;
		setSaving(true);
		setFailure(null);
		try {
			await onSave(draft);
			haptics.success();
			return true;
		} catch (cause) {
			const failed: MachineEditorFailure = cause instanceof MachineSaveError
				? { message: cause.message, field: cause.field }
				: { message: cause instanceof Error ? cause.message : "The machine could not be updated. Try again.", field: "host" };
			setFailure(failed);
			haptics.warning();
			// The message lands in a footer or under a field, which a screen reader
			// doesn't read out by itself; queued so it follows Save coming back.
			AccessibilityInfo.announceForAccessibilityWithOptions(failed.message, { queue: true });
			return false;
		} finally {
			setSaving(false);
		}
	}

	return {
		draft,
		/** Something differs from the machine as saved, which leaving would lose. */
		dirty,
		canSave,
		saving,
		failure,
		portError: portError(draft.config.httpPort),
		/** Account sync owns this machine's route and password; only its name and icon edit here. */
		accountManaged: Boolean(host.accountUserId),
		setName: (machineName: string) => edit((current) => ({ ...current, machineName })),
		setSymbol: (symbol: MachineSymbol) => edit((current) => ({ ...current, appearance: { ...current.appearance, symbol } })),
		setHost: setConfig("host"),
		setPort: setConfig("httpPort"),
		setPassword: setConfig("password"),
		setSecure: (secure: boolean) => edit((current) => ({ ...current, config: { ...current.config, secure } })),
		save,
	};
}

export type MachineEditor = ReturnType<typeof useMachineEditor>;
