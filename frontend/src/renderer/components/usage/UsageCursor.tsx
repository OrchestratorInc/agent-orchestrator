import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useCursorUsage, useSetCursorUsage } from "../../hooks/useUsageReport";
import { Button } from "../ui/button";

const CURSOR_ERRORS = {
	signed_out: "usage.cursor.error.signedOut",
	keychain: "usage.cursor.error.keychain",
	failed: "usage.cursor.error.failed",
} as const;

/**
 * Offers to count the usage of AO's Cursor sessions, which AO reads from
 * cursor.com with the Cursor login, and says when the last read failed.
 */
export function UsageCursor({ hasCursorSessions }: { hasCursorSessions: boolean }) {
	const { t } = useTranslation();
	const status = useCursorUsage().data;
	const setEnabled = useSetCursorUsage();
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState<string | null>(null);
	if (!status?.supported || (!status.enabled && !hasCursorSessions)) return null;

	const toggle = async (enabled: boolean) => {
		setBusy(true);
		setError(null);
		try {
			await setEnabled(enabled);
		} catch (cause) {
			setError(cause instanceof Error ? cause.message : String(cause));
		} finally {
			setBusy(false);
		}
	};
	const failure = status.enabled && status.error ? t(CURSOR_ERRORS[status.error]) : error;
	return (
		<div className="mb-4 flex flex-wrap items-baseline gap-x-2 gap-y-1 text-xs">
			<span className="text-muted-foreground">{status.enabled ? t("usage.cursor.on") : t("usage.cursor.off")}</span>
			{failure ? <span role="alert" className="text-error">{failure}</span> : null}
			<Button type="button" variant="link" disabled={busy} onClick={() => void toggle(!status.enabled)}>
				{status.enabled ? t("usage.cursor.turnOff") : t("usage.cursor.turnOn")}
			</Button>
		</div>
	);
}
