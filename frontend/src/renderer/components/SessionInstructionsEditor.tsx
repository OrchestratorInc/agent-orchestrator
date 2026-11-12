import { useState } from "react";
import { useTranslation } from "react-i18next";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "./ui/dialog";
import { DropdownMenuItem } from "./ui/dropdown-menu";

/** Session policy is loaded on open; saving changes only future provider deliveries. */
export function SessionInstructionsEditor({ sessionId }: { sessionId: string }) {
	const { t } = useTranslation();
	const [open, setOpen] = useState(false);
	const [text, setText] = useState("");
	const [busy, setBusy] = useState(false);
	const [error, setError] = useState("");
	const [loaded, setLoaded] = useState(false);
	const params = { path: { sessionId } };
	async function load() {
		setOpen(true); setBusy(true); setLoaded(false); setError("");
		try {
			const result = await apiClient.GET("/api/v1/sessions/{sessionId}/conversation/additional-instructions", { params });
			if (result.error) throw result.error;
			setText(result.data?.text ?? ""); setLoaded(true);
		} catch (err) { setError(apiErrorMessage(err)); }
		finally { setBusy(false); }
	}
	async function save(value: string) {
		setBusy(true); setError("");
		try {
			const result = await apiClient.PATCH("/api/v1/sessions/{sessionId}/conversation/additional-instructions", { params, body: { text: value } });
			if (result.error) throw result.error;
			setText(value); setOpen(false);
		} catch (err) { setError(apiErrorMessage(err)); }
		finally { setBusy(false); }
	}
	const tooLong = Array.from(text).length > 16000;
	return <>
		<DropdownMenuItem onSelect={(event) => { event.preventDefault(); void load(); }}>{t("sessionInstructions.title")}</DropdownMenuItem>
		<Dialog open={open} onOpenChange={(value) => { if (!busy) setOpen(value); }}>
			<DialogContent>
				<DialogHeader><DialogTitle>{t("sessionInstructions.title")}</DialogTitle></DialogHeader>
				<p className="text-sm text-muted-foreground">{t("sessionInstructions.description")}</p>
				<textarea aria-label={t("sessionInstructions.label")} className="min-h-48 w-full rounded border p-2" disabled={busy || !loaded} value={text} onChange={(event) => setText(event.target.value)} />
				<p className="text-xs">{t("sessionInstructions.count", { count: Array.from(text).length, limit: 16000 })}</p>
				{error || tooLong ? <p role="alert">{error || t("sessionInstructions.tooLong")}</p> : null}
				<div className="flex justify-end gap-2">
					<Button disabled={busy || !loaded} variant="outline" onClick={() => void save("")}>{t("sessionInstructions.clear")}</Button>
					<Button disabled={busy || !loaded || tooLong} onClick={() => void save(text)}>{t("sessionInstructions.save")}</Button>
				</div>
			</DialogContent>
		</Dialog>
	</>;
}
