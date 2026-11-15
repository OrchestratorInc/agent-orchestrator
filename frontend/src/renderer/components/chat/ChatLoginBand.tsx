import { Check, CircleAlert } from "@/components/app-icons";
import { useTranslation } from "react-i18next";
import { AgentAvatar } from "../AgentAvatar";
import { Button } from "../ui/button";

export type ChatLoginState = "signedOut" | "waiting" | "sending" | "loggedIn" | "failed";

export interface ChatLoginBandProps {
	harness: string;
	agent: string;
	host?: string;
	state: ChatLoginState;
	queued: boolean;
	onLogin: () => void;
	onSendAgain?: () => void;
	error?: string;
	signedOut?: boolean;
}

export function ChatLoginBand({ harness, agent, host, state, queued, onLogin, onSendAgain, error, signedOut }: ChatLoginBandProps) {
	const { t } = useTranslation();
	let mark;
	let title: string;
	let detail: string | undefined;
	let action;
	if (state === "waiting") {
		mark = <AgentAvatar provider={harness} decorative className="size-4 shrink-0" />;
		title = t("chat.login.finishOn", { page: t("settings.harness") });
		detail = queued ? t("chat.login.messageWaiting") : undefined;
		action = (
			<>
				<Button type="button" variant="ghost" size="sm" onClick={onLogin} className="px-3">{t("chat.login.openAgain")}</Button>
				{onSendAgain ? <Button type="button" variant="ghost" size="sm" onClick={onSendAgain} className="px-3">{t("chat.login.sendAgainAction")}</Button> : null}
			</>
		);
	} else if (state === "failed") {
		mark = <CircleAlert aria-hidden="true" className="size-4 shrink-0 text-destructive" />;
		title = t("chat.login.sendFailed");
		detail = error;
		action = signedOut
			? (
				<>
					{onSendAgain ? <Button type="button" variant="ghost" size="sm" onClick={onSendAgain} className="px-3">{t("chat.login.sendAgainAction")}</Button> : null}
					<Button type="button" variant="primary" size="sm" onClick={onLogin} className="px-3">{t("chat.login.action")}</Button>
				</>
			)
			: onSendAgain ? <Button type="button" variant="primary" size="sm" onClick={onSendAgain} className="px-3">{t("chat.login.sendAgainAction")}</Button> : undefined;
	} else if (state === "sending" && signedOut) {
		mark = <AgentAvatar provider={harness} decorative className="size-4 shrink-0" />;
		title = t("chat.login.status.sending");
	} else if (state === "sending" || state === "loggedIn") {
		mark = <Check aria-hidden="true" className="size-4 shrink-0 text-status-ready" />;
		title = t("chat.login.loggedIn", { agent });
		detail = state === "sending" ? t("chat.login.sendingMessage") : t("chat.login.sendAgain");
		action = state === "loggedIn" && onSendAgain
			? <Button type="button" variant="primary" size="sm" onClick={onSendAgain} className="px-3">{t("chat.login.sendAgainAction")}</Button>
			: undefined;
	} else {
		mark = <AgentAvatar provider={harness} decorative className="size-4 shrink-0" />;
		title = host ? t("chat.login.signedOutOnHost", { agent, host }) : t("chat.login.signedOut", { agent });
		detail = queued ? t("chat.login.sendsOnceIn") : t("chat.login.sendAgainOnceIn");
		action = (
			<>
				{onSendAgain ? <Button type="button" variant="ghost" size="sm" onClick={onSendAgain} className="px-3">{t("chat.login.sendAgainAction")}</Button> : null}
				<Button type="button" variant="primary" size="sm" onClick={onLogin} className="px-3">{t("chat.login.action")}</Button>
			</>
		);
	}
	return (
		<div data-testid="login-band" data-login-state={state} className="flex min-h-10 min-w-0 flex-wrap items-center gap-x-2.5 gap-y-1 py-1.5 pr-2.5 pl-3 text-sm leading-5">
			{mark}
			<p role="status" className="min-w-48 flex-1 text-pretty">
				<span className="text-foreground">{title}</span>
				{detail ? <span className="text-muted-foreground"> · {detail}</span> : null}
			</p>
			{action ? <div className="ml-auto flex shrink-0 items-center gap-1">{action}</div> : null}
		</div>
	);
}
