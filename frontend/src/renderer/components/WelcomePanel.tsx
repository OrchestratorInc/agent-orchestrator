import type { ReactNode } from "react";
import { CenterPanelShell } from "./CenterPanelShell";

/** Inset frame for the first-launch welcome board (import chooser). */
export function WelcomePanel({ children }: { children: ReactNode }) {
	return <CenterPanelShell topbar={false}>{children}</CenterPanelShell>;
}
