/** Shared shell panel spring: left sidebar, topbar padding, etc. The inspector rail uses its CSS twin (INSPECTOR_SPRING_EASING). */
export const SHELL_PANEL_SPRING = { type: "spring", stiffness: 420, damping: 40, mass: 0.6 } as const;

/** Interface v2's shell slide: a drawer ease-out, fast start and long gentle settle.
 * Its CSS twin is `--ease-shell-panel` in styles.css. */
export const SHELL_PANEL_DRAWER = { type: "tween", duration: 0.45, ease: [0.32, 0.72, 0, 1] } as const;

export function shellPanelTransition(newDesktopUi: boolean): typeof SHELL_PANEL_SPRING | typeof SHELL_PANEL_DRAWER {
	return newDesktopUi ? SHELL_PANEL_DRAWER : SHELL_PANEL_SPRING;
}
