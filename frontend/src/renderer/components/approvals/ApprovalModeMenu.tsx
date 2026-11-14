import { QueryClientContext } from "@tanstack/react-query";
import { useContext, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Check, ChevronRight, ShieldCheck } from "@/components/app-icons";
import { fetchApprovalPolicy, setApprovalMode, type ApprovalMode } from "../../lib/approvals";
import { DropdownMenuItem } from "../ui/dropdown-menu";

const MODES: ApprovalMode[] = ["off", "risky", "every-submit"];

/**
 * The session's approval mode, read when the Browser menu opens. The menu
 * lives outside any query provider in some hosts, so it reads directly and
 * refreshes shared queries only when a client exists.
 */
function useSessionApprovalMode(sessionId: string) {
	const queryClient = useContext(QueryClientContext);
	const [mode, setMode] = useState<ApprovalMode>();
	useEffect(() => {
		let cancelled = false;
		fetchApprovalPolicy(sessionId).then(
			(policy) => !cancelled && setMode(policy.mode),
			() => undefined,
		);
		return () => {
			cancelled = true;
		};
	}, [sessionId]);
	const choose = (next: ApprovalMode) =>
		void setApprovalMode(sessionId, next, queryClient).then((policy) => setMode(policy.mode), () => undefined);
	return { mode, choose };
}

/** Root row of the Browser menu: this session's approval mode. */
export function ApprovalModeMenuRow({ sessionId, onOpen }: { sessionId: string; onOpen: () => void }) {
	const { t } = useTranslation();
	const { mode } = useSessionApprovalMode(sessionId);
	return (
		<DropdownMenuItem
			className="gap-2"
			onSelect={(event) => {
				event.preventDefault();
				onOpen();
			}}
		>
			<ShieldCheck aria-hidden="true" className="size-icon-base shrink-0" />
			<span className="flex-1">{t("browser.approvals.label")}</span>
			{mode ? <span className="max-w-28 truncate text-caption text-passive">{t(`approvals.mode.${mode}`)}</span> : null}
			<ChevronRight aria-hidden="true" className="size-3.5 shrink-0 text-passive" />
		</DropdownMenuItem>
	);
}

/** The approval-mode sub-view: back, then the three modes. */
export function ApprovalModeMenuItems({ sessionId, onBack }: { sessionId: string; onBack: () => void }) {
	const { t } = useTranslation();
	const { mode, choose } = useSessionApprovalMode(sessionId);
	return (
		<>
			<DropdownMenuItem
				className="gap-1.5"
				onSelect={(event) => {
					event.preventDefault();
					onBack();
				}}
			>
				<ChevronRight aria-hidden="true" className="size-3.5 rotate-180 text-passive" />
				{t("browser.approvals.label")}
			</DropdownMenuItem>
			<div className="my-1 h-px bg-border" role="separator" />
			{MODES.map((option) => (
				<DropdownMenuItem key={option} className="gap-2" disabled={!mode} onSelect={() => choose(option)}>
					<span className="flex size-4 shrink-0 items-center justify-center">
						{mode === option ? <Check aria-hidden="true" className="text-accent" /> : null}
					</span>
					<span className="flex-1 truncate">{t(`approvals.mode.${option}`)}</span>
				</DropdownMenuItem>
			))}
		</>
	);
}
