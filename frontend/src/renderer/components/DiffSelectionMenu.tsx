import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react";
import { useTranslation } from "react-i18next";
import { formatDiffSelectionMessage, type DiffSelectionLine } from "../../shared/diff-selection";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { cn } from "../lib/utils";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Input } from "./ui/input";
import { Pressable } from "./ui/pressable";

export type DiffSelectionMenuProps = {
	sessionId: string;
	filePath: string;
	lines: DiffSelectionLine[];
	selectedText: string;
	open: boolean;
	position: { x: number; y: number };
	onOpenChange: (open: boolean) => void;
	scope?: string;
	workspaceVersion?: string;
	fileFingerprint?: string;
};

type Mode = "actions" | "input";
type SendStatus = "idle" | "sending" | "sent" | "error";

const SENT_AUTO_CLOSE_MS = 2_000;

export function DiffSelectionMenu({
	sessionId,
	filePath,
	lines,
	selectedText,
	open,
	position,
	onOpenChange,
	scope,
	workspaceVersion,
	fileFingerprint,
}: DiffSelectionMenuProps) {
	const { t } = useTranslation();
	const [mode, setMode] = useState<Mode>("actions");
	const [status, setStatus] = useState<SendStatus>("idle");
	const [errorMessage, setErrorMessage] = useState("");
	const [inputValue, setInputValue] = useState("");
	const inputRef = useRef<HTMLInputElement | null>(null);
	const sentTimerRef = useRef<number | null>(null);
	const openGenerationRef = useRef(0);

	const clearSentTimer = useCallback(() => {
		if (sentTimerRef.current !== null) {
			window.clearTimeout(sentTimerRef.current);
			sentTimerRef.current = null;
		}
	}, []);

	const resetSendState = useCallback(() => {
		clearSentTimer();
		setStatus("idle");
		setErrorMessage("");
	}, [clearSentTimer]);

	useEffect(() => {
		openGenerationRef.current += 1;
		if (!open) return;
		clearSentTimer();
		setMode("actions");
		setStatus("idle");
		setErrorMessage("");
		setInputValue("");
	}, [open, clearSentTimer]);

	useEffect(() => clearSentTimer, [clearSentTimer]);

	useEffect(() => {
		if (mode === "input") inputRef.current?.focus();
	}, [mode]);

	const send = useCallback(
		async (instruction: string) => {
			const openGeneration = openGenerationRef.current;
			clearSentTimer();
			setStatus("sending");
			setErrorMessage("");
			const message = formatDiffSelectionMessage({ instruction, filePath, lines, scope, workspaceVersion, fileFingerprint });
			try {
				const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/send", {
					params: { path: { sessionId } },
					body: { message, userAuthored: true },
				});
				if (openGeneration !== openGenerationRef.current) return;
				if (error) {
					setStatus("error");
					setErrorMessage(apiErrorMessage(error, t("diffSelection.error.send")));
					return;
				}
				setStatus("sent");
				sentTimerRef.current = window.setTimeout(() => {
					sentTimerRef.current = null;
					onOpenChange(false);
				}, SENT_AUTO_CLOSE_MS);
			} catch (thrown) {
				if (openGeneration !== openGenerationRef.current) return;
				setStatus("error");
				setErrorMessage(apiErrorMessage(thrown, t("diffSelection.error.send")));
			}
		},
		[clearSentTimer, fileFingerprint, filePath, lines, onOpenChange, scope, sessionId, t, workspaceVersion],
	);

	const handleCopy = useCallback(() => {
		void navigator.clipboard?.writeText(selectedText);
		onOpenChange(false);
	}, [onOpenChange, selectedText]);

	const submitInput = useCallback(() => {
		const trimmed = inputValue.trim();
		if (!trimmed || status === "sending") return;
		void send(trimmed);
	}, [inputValue, send, status]);

	const handleInputKeyDown = useCallback(
		(event: KeyboardEvent<HTMLInputElement>) => {
			event.stopPropagation();
			if (event.key === "Escape") {
				event.preventDefault();
				resetSendState();
				setMode("actions");
				return;
			}
			if (event.key === "Enter") {
				event.preventDefault();
				submitInput();
			}
		},
		[resetSendState, submitInput],
	);

	const statusLabel =
		status === "sending"
			? t("diffSelection.sending")
			: status === "sent"
				? t("diffSelection.sent")
				: status === "error"
					? errorMessage
					: "";

	return (
		<DropdownMenu modal={false} open={open} onOpenChange={onOpenChange}>
			<DropdownMenuTrigger asChild>
				<Pressable
					aria-hidden="true"
					style={{
						border: 0,
						height: 0,
						left: position.x,
						opacity: 0,
						padding: 0,
						pointerEvents: "none",
						position: "fixed",
						top: position.y,
						width: 0,
					}}
					tabIndex={-1}
				/>
			</DropdownMenuTrigger>
			<DropdownMenuContent
				align="start"
				className="min-w-56"
				onCloseAutoFocus={(event) => event.preventDefault()}
				onEscapeKeyDown={(event) => {
					if (mode !== "input") return;
					event.preventDefault();
					resetSendState();
					setMode("actions");
				}}
				side="right"
				sideOffset={2}
			>
				{mode === "actions" ? (
					<>
						<DropdownMenuItem
							disabled={selectedText.length === 0}
							onSelect={(event) => {
								event.preventDefault();
								resetSendState();
								handleCopy();
							}}
						>
							{t("diffSelection.copy")}
						</DropdownMenuItem>
						<DropdownMenuSeparator />
						<DropdownMenuItem
							disabled={status === "sending"}
							onSelect={(event) => {
								event.preventDefault();
								void send(t("diffSelection.explainInstruction"));
							}}
						>
							{t("diffSelection.explain")}
						</DropdownMenuItem>
						<DropdownMenuItem
							disabled={status === "sending"}
							onSelect={(event) => {
								event.preventDefault();
								resetSendState();
								setMode("input");
							}}
						>
							{t("diffSelection.makeChanges")}
						</DropdownMenuItem>
					</>
				) : (
					<div className="p-1.5">
						<Input
							aria-label={t("diffSelection.describeChange")}
							disabled={status === "sending"}
							onChange={(event) => setInputValue(event.target.value)}
							onKeyDown={handleInputKeyDown}
							placeholder={t("diffSelection.placeholder")}
							ref={inputRef}
							value={inputValue}
						/>
					</div>
				)}
				{statusLabel ? (
					<div className={cn("px-2 py-1.5 text-caption", status === "error" ? "text-error" : "text-passive")}>
						{statusLabel}
					</div>
				) : null}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
