import { Bot, EyeOff, MessageSquare } from "lucide-react";
import { useRef, type KeyboardEvent } from "react";
import { Trans, useTranslation } from "react-i18next";
import { Button } from "../ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "../ui/tooltip";

export type ComposerMode = "agent" | "aside";

/**
 * Aside mode's footer: what the dashed edge means, in the words an aside row
 * uses. While this device hides asides, it says so instead, with a way back,
 * so a posted aside never seems to vanish.
 */
export function AsideComposerHint({ hidden, onShow }: { hidden?: boolean; onShow?: () => void }) {
	const { t } = useTranslation();
	if (hidden && onShow) {
		return (
			<span className="ml-1 inline-flex min-w-0 items-center gap-1 truncate text-caption text-muted-foreground" data-aside-hidden-notice="">
				<EyeOff aria-hidden="true" className="size-3 shrink-0" />
				<span className="truncate">
					<Trans
						t={t}
						i18nKey="aside.hiddenNotice"
						components={{ show: <Button className="h-auto p-0 text-caption" onClick={onShow} type="button" variant="link" /> }}
					/>
				</span>
			</span>
		);
	}
	return (
		<span className="ml-1 inline-flex min-w-0 items-center gap-1 truncate text-caption text-muted-foreground" data-aside-composer-hint="">
			<EyeOff aria-hidden="true" className="size-3 shrink-0" />
			{t("aside.hiddenFromAgent")}
		</span>
	);
}

/**
 * The composer's two modes in a shared cloud session: Agent writes to the
 * agent, Aside writes to the people here and never reaches the agent. A radio
 * pair (arrow keys move between them); Shift+Tab in the editor switches too,
 * and each mode keeps its own draft. While an approval or question waits on
 * the agent and someone is writing an aside, Agent carries a needs-you dot.
 */
export function ComposerModeToggle({ mode, onChange, disabled, agentNeedsYou }: {
	mode: ComposerMode;
	onChange: (mode: ComposerMode) => void;
	disabled?: boolean;
	agentNeedsYou?: boolean;
}) {
	const { t } = useTranslation();
	const refs = useRef<Partial<Record<ComposerMode, HTMLButtonElement | null>>>({});
	const options = [
		{ value: "agent" as const, label: t("aside.mode.agent"), Icon: Bot },
		{ value: "aside" as const, label: t("aside.mode.aside"), Icon: MessageSquare },
	];
	const move = (event: KeyboardEvent<HTMLDivElement>) => {
		if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) return;
		event.preventDefault();
		const next: ComposerMode = mode === "agent" ? "aside" : "agent";
		onChange(next);
		refs.current[next]?.focus();
	};
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<div
					aria-label={t("aside.mode.label")}
					className="inline-flex shrink-0 items-center gap-0.5"
					data-composer-mode={mode}
					onKeyDown={move}
					role="radiogroup"
				>
					{options.map(({ value, label, Icon }) => {
						const needsYou = value === "agent" && agentNeedsYou && mode !== "agent";
						return (
							<Button
								aria-checked={mode === value}
								aria-label={needsYou ? t("aside.mode.agentNeedsYou") : undefined}
								className="relative"
								data-needs-you={needsYou ? "" : undefined}
								disabled={disabled}
								key={value}
								onClick={() => { if (mode !== value) onChange(value); }}
								// The editor keeps focus, so typing carries on in the other mode's draft.
								onMouseDown={(event) => event.preventDefault()}
								ref={(node: HTMLButtonElement | null) => { refs.current[value] = node; }}
								role="radio"
								size="xs"
								tabIndex={mode === value ? 0 : -1}
								type="button"
								variant="quiet"
							>
								<Icon aria-hidden="true" className="size-3" />
								{label}
								{needsYou ? <span aria-hidden="true" className="absolute top-0.5 right-0.5 size-1.5 rounded-full bg-status-needs-you" /> : null}
							</Button>
						);
					})}
				</div>
			</TooltipTrigger>
			<TooltipContent side="top">{t("aside.mode.shortcut")}</TooltipContent>
		</Tooltip>
	);
}
