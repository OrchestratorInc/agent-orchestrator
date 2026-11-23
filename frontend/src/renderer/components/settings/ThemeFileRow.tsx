import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useCustomThemes, useDeleteCustomTheme, useImportCustomTheme } from "../../hooks/useCustomTheme";
import { apiErrorCode, apiErrorMessage } from "../../lib/api-client";
import { customThemeStyle, isCustomThemeStyle } from "../../lib/custom-theme";
import { builtInThemeFile, serializeThemeFile } from "../../lib/theme-file";
import { DEFAULT_THEME_STYLE, getThemeDefinition, isThemeStyle } from "../../lib/theme-palettes";
import { useUiStore } from "../../stores/ui-store";
import { ConfirmDialog } from "../ConfirmDialog";
import { Button } from "../ui/button";
import { SettingsRow } from "./SettingsRow";

const COPIED_MS = 1600;
// A theme's CSS is at most 2 MiB (MaxCSSBytes in service/apptheme), so a file
// twice that size cannot be a theme and is not read into the dialog.
const MAX_THEME_FILE_BYTES = 4 * 1024 * 1024;

/**
 * Theme files in Settings > Appearance: copy the theme on screen as a file to
 * share, import one someone shared, and edit or delete a custom theme. A
 * built-in theme copies as a file too, as a starting point; importing it back
 * makes a custom theme. Agents make the same files with `ao theme`.
 */
export function ThemeFileRow() {
	const { t } = useTranslation();
	const themeStyle = useUiStore((state) => state.themeStyle);
	const setThemeStyle = useUiStore((state) => state.setThemeStyle);
	const { data } = useCustomThemes();
	const importTheme = useImportCustomTheme();
	const { remove, removing, error: deleteError } = useDeleteCustomTheme();

	const custom = isCustomThemeStyle(themeStyle) ? data?.themes.find((theme) => customThemeStyle(theme.id) === themeStyle) : undefined;
	// The mode outlives the dialog, so its wording holds while it closes.
	const [mode, setMode] = useState<"import" | "edit">("import");
	const [editing, setEditing] = useState(false);
	const [text, setText] = useState("");
	const [error, setError] = useState<string | null>(null);
	// Set once the daemon says a theme already has this file's id: the next
	// confirm replaces it, and the button says so.
	const [replacing, setReplacing] = useState(false);
	// What the last import left out of the file, such as an unknown color role.
	const [leftOut, setLeftOut] = useState<string[]>([]);
	// The theme a dialog was opened for. An agent can switch themes while one
	// is open; Save and Delete must still act on the theme the user chose.
	const [editingId, setEditingId] = useState<string | null>(null);
	const [deleting, setDeleting] = useState<{ id: string; name: string } | null>(null);
	const [confirmingDelete, setConfirmingDelete] = useState(false);
	const [copied, setCopied] = useState(false);
	const textRef = useRef<HTMLTextAreaElement>(null);
	const fileRef = useRef<HTMLInputElement>(null);

	useEffect(() => {
		if (!copied) return;
		const timer = window.setTimeout(() => setCopied(false), COPIED_MS);
		return () => window.clearTimeout(timer);
	}, [copied]);

	const currentFile = () =>
		serializeThemeFile(custom ?? builtInThemeFile(getThemeDefinition(isThemeStyle(themeStyle) ? themeStyle : DEFAULT_THEME_STYLE)));

	const open = (next: "import" | "edit") => {
		setText(next === "edit" ? currentFile() : "");
		setError(null);
		setReplacing(false);
		setLeftOut([]);
		setEditingId(next === "edit" ? (custom?.id ?? null) : null);
		importTheme.reset();
		setMode(next);
		setEditing(true);
	};

	const copy = async () => {
		try {
			await navigator.clipboard.writeText(currentFile());
			setCopied(true);
		} catch {
			// Clipboard blocked: the Edit dialog still shows the file to copy by hand.
		}
	};

	const submit = () => {
		let file: unknown;
		try {
			file = JSON.parse(text);
		} catch {
			file = null;
		}
		if (!file || typeof file !== "object" || Array.isArray(file)) {
			setError(t("settings.themeFile.invalid"));
			return;
		}
		setError(null);
		importTheme.mutate(
			// Saving an edit replaces the theme being edited, and only that one:
			// its id is fixed here, so an id typed into the file cannot overwrite
			// another theme. An import only replaces a theme after the user has
			// been told it would.
			{
				file: file as Record<string, unknown>,
				keepExisting: mode === "import" && !replacing,
				...(mode === "edit" && editingId ? { id: editingId } : {}),
			},
			{
				onSuccess: (imported) => {
					setEditing(false);
					setLeftOut(imported.warnings ?? []);
					setThemeStyle(customThemeStyle(imported.theme.id));
				},
				onError: (cause) => {
					if (apiErrorCode(cause) === "CUSTOM_THEME_EXISTS") {
						setReplacing(true);
						setError(t("settings.themeFile.exists"));
						return;
					}
					setError(apiErrorMessage(cause));
				},
			},
		);
	};

	const chooseFile = async (file: File | undefined) => {
		if (!file) return;
		if (file.size > MAX_THEME_FILE_BYTES) {
			setError(t("settings.themeFile.tooLarge"));
			return;
		}
		setText(await file.text());
		setError(null);
		setReplacing(false);
	};

	return (
		<>
			<SettingsRow
				label={t("settings.themeFile")}
				description={
					deleteError ? (
						<span role="alert" className="text-error">{deleteError}</span>
					) : leftOut.length > 0 ? (
						<span role="status">{t("settings.themeFile.leftOut", { details: leftOut.join("; ") })}</span>
					) : (
						t("settings.themeFile.description")
					)
				}
			>
				<div className="flex items-center gap-0.5">
					<Button onClick={() => open("import")} size="sm" type="button" variant="ghost">
						{t("settings.themeFile.import")}
					</Button>
					<Button onClick={() => void copy()} size="sm" type="button" variant="ghost">
						<span aria-live="polite">{copied ? t("settings.themeFile.copied") : t("settings.themeFile.copy")}</span>
					</Button>
					{custom ? (
						<>
							<Button onClick={() => open("edit")} size="sm" type="button" variant="ghost">
								{t("settings.themeFile.edit")}
							</Button>
							<Button
								disabled={removing}
								onClick={() => {
									setDeleting({ id: custom.id, name: custom.name });
									setConfirmingDelete(true);
								}}
								size="sm"
								type="button"
								variant="destructive-ghost"
							>
								{t("settings.themeFile.delete")}
							</Button>
						</>
					) : null}
				</div>
			</SettingsRow>

			<ConfirmDialog
				open={editing}
				title={t(mode === "edit" ? "settings.themeFile.editTitle" : "settings.themeFile.importTitle")}
				description={t(mode === "edit" ? "settings.themeFile.editDescription" : "settings.themeFile.importDescription")}
				confirmLabel={t(mode === "edit" ? "settings.themeFile.save" : replacing ? "settings.themeFile.replace" : "settings.themeFile.import")}
				confirmDisabled={text.trim() === ""}
				busy={importTheme.isPending}
				error={error}
				initialFocusRef={textRef}
				onConfirm={submit}
				onOpenChange={setEditing}
			>
				<div className="flex flex-col gap-2">
					<textarea
						ref={textRef}
						aria-label={t("settings.themeFile")}
						autoCapitalize="off"
						autoCorrect="off"
						className="settings-field-control h-56 resize-none overflow-y-auto rounded-md! py-2.5 font-mono text-xs leading-5"
						onChange={(event) => {
							setText(event.target.value);
							setError(null);
							setReplacing(false);
						}}
						spellCheck={false}
						value={text}
					/>
					{mode === "import" ? (
						<div>
							<input
								ref={fileRef}
								accept="application/json,.json"
								className="hidden"
								onChange={(event) => {
									void chooseFile(event.target.files?.[0]);
									event.target.value = "";
								}}
								type="file"
							/>
							<Button onClick={() => fileRef.current?.click()} size="sm" type="button" variant="ghost">
								{t("settings.themeFile.choose")}
							</Button>
						</div>
					) : null}
				</div>
			</ConfirmDialog>

			<ConfirmDialog
				open={confirmingDelete}
				title={t("settings.themeFile.deleteTitle")}
				description={t("settings.themeFile.deleteDescription", { name: deleting?.name ?? "" })}
				confirmLabel={t("settings.themeFile.delete")}
				destructive
				onConfirm={() => {
					setConfirmingDelete(false);
					if (deleting) remove(deleting.id);
				}}
				onOpenChange={setConfirmingDelete}
			/>
		</>
	);
}
