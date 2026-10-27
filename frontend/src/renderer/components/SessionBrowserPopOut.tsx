import { type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { usePopOutDialog } from "../hooks/usePopOutDialog";
import { useWindowFullScreen } from "../hooks/useWindowFullScreen";
import { hidesShellTopbar } from "../lib/platform";
import { cn } from "../lib/utils";

export function SessionBrowserPopOut({ children, onClose, onTopbarHost, phase }: {
	children: ReactNode;
	onClose?: () => void;
	onTopbarHost: (host: HTMLDivElement | null) => void;
	phase: "mounting" | "open";
}) {
	const { t } = useTranslation();
	const isNativeFullScreen = useWindowFullScreen();
	const macWindowed = hidesShellTopbar() && !isNativeFullScreen;
	const dialogProps = usePopOutDialog<HTMLDivElement>(onClose);
	return <div {...dialogProps} aria-label={t("common.maximize")} className={cn("browser-popout-overlay outline-none", macWindowed && "browser-popout-overlay--mac-windowed")} data-phase={phase}>
		<div aria-hidden="true" className="browser-popout-backdrop" />
		<div className={cn("browser-popout-titlebar browser-panel__topbar-host", macWindowed && "browser-popout-titlebar--mac-windowed")} data-testid="browser-popout-topbar" ref={onTopbarHost} />
		<div className="browser-popout-frame">{children}</div>
	</div>;
}
