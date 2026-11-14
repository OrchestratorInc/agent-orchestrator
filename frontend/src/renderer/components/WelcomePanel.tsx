import type { ReactNode } from "react";
import { CenterPanelShell } from "./CenterPanelShell";

export function WelcomePanel({ children }: { children: ReactNode }) {
	return <CenterPanelShell topbar={false}>{children}</CenterPanelShell>;
}
