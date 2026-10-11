import { useTranslation } from "react-i18next";
import { cn } from "../lib/utils";
import { MultiStepLoader } from "./ui/multi-step-loader";

export function CloudSessionStartupLoader({
	activeIndex = 0,
	completed = false,
	startupNote,
}: {
	activeIndex?: number;
	completed?: boolean;
	startupNote?: string;
}) {
	const { t } = useTranslation();
	const loader = <MultiStepLoader
		ariaLabel={t("terminal.sessionLoader.label")}
		activeIndex={completed ? 3 : activeIndex}
		className={startupNote ? "w-full max-w-none" : undefined}
		complete={completed}
		steps={[
			t("terminal.sessionLoader.workspace"),
			t("terminal.sessionLoader.worker"),
			t("terminal.sessionLoader.repositoryAgent"),
			t("terminal.sessionLoader.terminal"),
		]}
	/>;
	return (
		<div
			// Cover session chrome while staying below app dialogs and menus.
			className={cn("absolute inset-0 z-chrome grid place-items-center bg-background", completed && "cloud-session-loader--complete pointer-events-none")}
			data-testid="cloud-session-loader-screen"
		>
			{startupNote ? (
				<div className="flex w-80 max-w-[calc(100%-2rem)] flex-col gap-4">
					{loader}
					<p className="text-xs leading-relaxed text-muted-foreground" data-testid="cloud-session-startup-note">
						{startupNote} {t("cloud.startupError.stillRetrying")}
					</p>
				</div>
			) : loader}
		</div>
	);
}
