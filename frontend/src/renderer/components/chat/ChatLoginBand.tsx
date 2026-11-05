import { Check, Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { AgentAvatar } from "../AgentAvatar";
import { Button } from "../ui/button";

/**
 * Where a refused login stands: still signed out, the Agents page left open
 * without a login, logged in and sending the message again, or logged in with
 * nothing sent again (the message did work, so the user sends it).
 */
export type ChatLoginState = "signedOut" | "waiting" | "sending" | "loggedIn";

export interface ChatLoginBandProps {
	/** The agent whose mark leads the band. */
	harness: string;
	/** The agent's display name. */
	agent: string;
	/** The remote host whose login it is; omitted for this computer. */
	host?: string;
	state: ChatLoginState;
	/** The refused message sends again by itself once the login takes. */
	queued: boolean;
	onLogin: () => void;
}

/**
 * The login a refused chat needs, as a band over the chat box: what is wrong
 * and the one action, then how the login is going. It sits where the user was
 * about to write, so the timeline keeps only a quiet line under the message.
 */
export function ChatLoginBand({ harness, agent, host, state, queued, onLogin }: ChatLoginBandProps) {
	const { t } = useTranslation();
	let mark;
	let title: string;
	let detail: string | undefined;
	let action;
	if (state === "waiting") {
		mark = <Loader2 aria-hidden="true" className="size-4 shrink-0 animate-spin text-muted-foreground" />;
		title = t("chat.login.finishOn", { page: t("settings.harness") });
		detail = queued ? t("chat.login.messageWaiting") : undefined;
		action = <Button type="button" variant="ghost" size="sm" onClick={onLogin} className="px-3">{t("chat.login.openAgain")}</Button>;
	} else if (state === "sending" || state === "loggedIn") {
		mark = <Check aria-hidden="true" className="size-4 shrink-0 text-status-ready" />;
		title = t("chat.login.loggedIn", { agent });
		detail = state === "sending" ? t("chat.login.sendingMessage") : t("chat.login.sendAgain");
	} else {
		mark = <AgentAvatar provider={harness} decorative className="size-4 shrink-0" />;
		title = host ? t("chat.login.signedOutOnHost", { agent, host }) : t("chat.login.signedOut", { agent });
		detail = queued ? t("chat.login.sendsOnceIn") : t("chat.login.sendAgainOnceIn");
		action = <Button type="button" variant="primary" size="sm" onClick={onLogin} className="px-3">{t("chat.login.action")}</Button>;
	}
	return (
		<div data-testid="login-band" data-login-state={state} className="flex min-h-10 min-w-0 items-center gap-2.5 pt-1 pr-1 pb-2.5 pl-3 text-sm leading-5">
			{mark}
			<p role="status" className="min-w-0 flex-1 text-pretty">
				<span className="text-foreground">{title}</span>
				{detail ? <span className="text-muted-foreground"> · {detail}</span> : null}
			</p>
			{action}
		</div>
	);
}
