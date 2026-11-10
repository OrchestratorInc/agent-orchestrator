import { useRef, useSyncExternalStore, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { OperatorVerbRequest } from "../../../main/operator-ipc";
import { useWorkflowAction, useWorkflowScope } from "../../hooks/useWorkflows";
import { getEventsConnectionState, subscribeEventsConnection } from "../../lib/events-connection";
import { useWorkflowUi } from "../../stores/workflow-ui-store";
import { Button } from "../ui/button";

/** 未得到成功回执前保留原请求；页面版本变化不能把超时重试变成第二次操作。 */
export function WorkflowAction({ verb, path, payload, children, disabled, withKey = true, keyField = "idempotencyKey", requestKey, onSuccess }: {
	verb: OperatorVerbRequest["verb"]; path: string; payload: object; children: ReactNode;
	disabled?: boolean; withKey?: boolean; keyField?: "idempotencyKey" | "clientMessageId"; requestKey?: string; onSuccess?: (body: unknown) => void;
}) {
	const action = useWorkflowAction();
	const { t } = useTranslation();
	const scope = useWorkflowScope();
	const connection = useSyncExternalStore(subscribeEventsConnection, getEventsConnectionState);
	const mode = "mode" in payload ? String(payload.mode) : "verdict" in payload ? String(payload.verdict) : "approve" in payload ? String(payload.approve) : "";
	// 同一路径管理多个策略时，原请求只能在对应策略上重放。
	const key = `${scope[1]}:${verb}:${path}:${requestKey ?? mode}`;
	const latestKey = useRef(key);
	latestKey.current = key;
	const prior = useWorkflowUi(state => state.requests[key]);
	const serialized = JSON.stringify(payload);
	const latestPayload = useRef(serialized);
	latestPayload.current = serialized;
	const changed = !!prior && prior.payload !== serialized;
	const send = () => {
		const saved = useWorkflowUi.getState().requests[key];
		const request = saved?.request ?? {
			verb, operationPath: path, body: JSON.stringify(withKey ? { ...payload, [keyField]: crypto.randomUUID() } : payload),
		};
		useWorkflowUi.getState().rememberRequest(key, saved?.payload ?? serialized, request);
		action.mutate(request, { onSuccess: body => {
			useWorkflowUi.getState().forgetRequest(key);
			if (latestKey.current === key && latestPayload.current === (saved?.payload ?? serialized)) onSuccess?.(body);
		}, onError: error => {
			const status = "status" in error ? error.status : undefined;
			if (status === 400 || status === 403 || status === 404 || status === 409 || status === 422 || (status === 499 && "code" in error && error.code === "OPERATOR_CANCELLED")) useWorkflowUi.getState().forgetRequest(key);
		} });
	};
	return <div className="inline-flex flex-col items-start gap-1">
		<Button size="sm" variant="outline" disabled={(!prior && disabled) || action.isPending || connection !== "connected" || !window.ao} onClick={send}>{prior ? t("workflow.retryOriginal", "重试原请求") : children}</Button>
		{changed && <span className="text-xs text-muted-foreground">{t("workflow.pendingOriginal", "原请求尚未确认，重试将使用原目标和正文。")}</span>}
		{action.error && <span role="alert" className="text-xs text-destructive">{action.error.message}</span>}
	</div>;
}
