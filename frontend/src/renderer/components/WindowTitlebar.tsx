import { useCanGoBack, useRouter } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import {
  ArrowLeft,
  ArrowRight,
  Copy,
  Minus,
  PanelLeft,
  Square,
  X,
} from "@/components/app-icons";
import { useEffect, useState } from "react";
import { sidebarIsVisible, useUiStore } from "../stores/ui-store";
import { useCanGoForward } from "./TitlebarNav";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Pressable } from "./ui/pressable";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const isWindows =
  typeof navigator !== "undefined" &&
  /win/i.test(
    (navigator as Navigator & { userAgentData?: { platform?: string } })
      .userAgentData?.platform ??
      navigator.platform ??
      "",
  );

type MenuKey = "view" | "help";

const act = (action: string) => () => {
  void window.ao?.menu?.action(action);
};

function TopMenu({
  id,
  label,
  openMenu,
  setOpenMenu,
  children,
}: {
  id: MenuKey;
  label: string;
  openMenu: MenuKey | null;
  setOpenMenu: (key: MenuKey | null) => void;
  children: React.ReactNode;
}) {
  return (
    <DropdownMenu
      modal={false}
      open={openMenu === id}
      onOpenChange={(open) => setOpenMenu(open ? id : null)}
    >
      <DropdownMenuTrigger asChild>
        <Pressable
          className="window-titlebar__menu-btn"
          data-active={openMenu === id ? "" : undefined}
          onMouseEnter={() => setOpenMenu(openMenu === null ? null : id)}
          type="button"
        >
          {label}
        </Pressable>
      </DropdownMenuTrigger>
      <DropdownMenuContent
        align="start"
        className="window-titlebar__menu"
        data-browser-native-overlay="true"
        sideOffset={4}
      >
        {children}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function WindowControls({
  isMaximized,
  t,
}: {
  isMaximized: boolean;
  t: TFunction;
}) {
  return (
    <div
      aria-label={t("titlebar.window")}
      className="window-titlebar__controls"
      role="group"
    >
      <Pressable
        aria-label={t("titlebar.minimize")}
        className="window-titlebar__control"
        onClick={act("window.minimize")}
        type="button"
      >
        <Minus aria-hidden="true" className="window-titlebar__control-icon" />
      </Pressable>
      <Pressable
        aria-label={t("titlebar.maximize")}
        className="window-titlebar__control"
        onClick={act("window.maximize")}
        type="button"
      >
        {isMaximized ? (
          <Copy
            aria-hidden="true"
            className="window-titlebar__control-icon window-titlebar__control-icon--maximize"
          />
        ) : (
          <Square
            aria-hidden="true"
            className="window-titlebar__control-icon window-titlebar__control-icon--maximize"
          />
        )}
      </Pressable>
      <Pressable
        aria-label={t("titlebar.close")}
        className="window-titlebar__control window-titlebar__control--close"
        onClick={act("window.close")}
        type="button"
      >
        <X aria-hidden="true" className="window-titlebar__control-icon" />
      </Pressable>
    </div>
  );
}

export function WindowTitlebar({ onBack }: { onBack?: () => void }) {
  const { t } = useTranslation();
  const toggleSidebar = useUiStore((state) => state.toggleSidebar);
  const isSidebarOpen = useUiStore(sidebarIsVisible);
  const router = useRouter();
  const canGoBack = useCanGoBack();
  const canGoForward = useCanGoForward();
  const [openMenu, setOpenMenu] = useState<MenuKey | null>(null);
  const [isMaximized, setIsMaximized] = useState(false);

  useEffect(() => {
    if (!isWindows) return;
    let active = true;
    void window.ao?.window?.isMaximized().then((maximized) => {
      if (active) setIsMaximized(maximized);
    });
    const unsubscribe = window.ao?.window?.onMaximized((maximized) =>
      setIsMaximized(maximized),
    );
    return () => {
      active = false;
      unsubscribe?.();
    };
  }, []);

  useEffect(() => {
    const onShellUse = (event: Event) => {
      const target = event.target as HTMLElement | null;
      if (target?.closest('[class*="window-titlebar"]')) return;
      if (target?.closest('[data-testid="browser-panel"]')) return;
      if (target?.closest('[data-testid="browser-address-bar"], .browser-panel__topbar-host')) return;
      void window.ao?.menu?.notifyShellFocus();
    };
    document.addEventListener("focusin", onShellUse);
    document.addEventListener("pointerdown", onShellUse, true);
    return () => {
      document.removeEventListener("focusin", onShellUse);
      document.removeEventListener("pointerdown", onShellUse, true);
    };
  }, []);

  if (!isWindows) return null;

  return (
    <header className="window-titlebar">
      <Tooltip>
        <TooltipTrigger asChild>
          <Pressable
            aria-label={
              isSidebarOpen
                ? t("shell.collapseSidebar")
                : t("shell.expandSidebar")
            }
            className="window-titlebar__toggle"
            onClick={toggleSidebar}
            type="button"
          >
            <PanelLeft
              aria-hidden="true"
              className="window-titlebar__toggle-icon"
            />
          </Pressable>
        </TooltipTrigger>
        <TooltipContent shortcut="toggle-sidebar" side="bottom">
          {isSidebarOpen
            ? t("shell.collapseSidebar")
            : t("shell.expandSidebar")}
        </TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex">
            <Pressable
              aria-label={t("titlebar.goBack")}
              className="window-titlebar__toggle"
              disabled={!onBack && !canGoBack}
              onClick={onBack ?? (() => router.history.back())}
              type="button"
            >
              <ArrowLeft
                aria-hidden="true"
                className="window-titlebar__toggle-icon"
              />
            </Pressable>
          </span>
        </TooltipTrigger>
        <TooltipContent shortcut="history-back" side="bottom">{t("titlebar.goBack")}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="inline-flex">
            <Pressable
              aria-label={t("titlebar.goForward")}
              className="window-titlebar__toggle"
              disabled={!canGoForward}
              onClick={() => router.history.forward()}
              type="button"
            >
              <ArrowRight
                aria-hidden="true"
                className="window-titlebar__toggle-icon"
              />
            </Pressable>
          </span>
        </TooltipTrigger>
        <TooltipContent shortcut="history-forward" side="bottom">{t("titlebar.goForward")}</TooltipContent>
      </Tooltip>
      <nav className="window-titlebar__menus">
        <TopMenu
          id="view"
          label={t("titlebar.view")}
          openMenu={openMenu}
          setOpenMenu={setOpenMenu}
        >
          {import.meta.env.DEV && (
            <>
              <DropdownMenuItem onSelect={act("view.reload")}>
                {t("titlebar.reload")}
                <DropdownMenuShortcut>Ctrl+R</DropdownMenuShortcut>
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={act("view.devtools")}>
                {t("titlebar.devtools")}
                <DropdownMenuShortcut>Ctrl+Shift+I</DropdownMenuShortcut>
              </DropdownMenuItem>
              <DropdownMenuSeparator />
            </>
          )}
          <DropdownMenuItem onSelect={act("view.zoomIn")}>
            {t("titlebar.zoomIn")}
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={act("view.zoomOut")}>
            {t("titlebar.zoomOut")}
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={act("view.zoomReset")}>
            {t("titlebar.zoomReset")}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={act("view.fullscreen")}>
            {t("titlebar.fullscreen")}
            <DropdownMenuShortcut>F11</DropdownMenuShortcut>
          </DropdownMenuItem>
        </TopMenu>

        <TopMenu
          id="help"
          label={t("titlebar.help")}
          openMenu={openMenu}
          setOpenMenu={setOpenMenu}
        >
          <DropdownMenuItem onSelect={act("help.shortcuts")}>
            {t("settings.keyboardShortcuts")}
            <DropdownMenuShortcut>Ctrl+/</DropdownMenuShortcut>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem onSelect={act("help.about")}>
            {t("titlebar.about")}
          </DropdownMenuItem>
        </TopMenu>
      </nav>
      <div className="window-titlebar__spacer" />
      <WindowControls isMaximized={isMaximized} t={t} />
    </header>
  );
}
