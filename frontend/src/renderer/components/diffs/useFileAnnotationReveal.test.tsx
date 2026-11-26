import { act, fireEvent, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { FileAnnotationRange } from "../../../shared/chat-file-reference";
import { useFileAnnotationReveal } from "./useFileAnnotationReveal";

function setup(markup: string, range: FileAnnotationRange, offscreen = false, ready = true) {
	const container = document.createElement("div");
	document.body.append(container);
	const host = document.createElement("diffs-container");
	container.append(host);
	const root = host.attachShadow({ mode: "open" });
	const scroll = vi.fn();
	const redraw = (markup: string) => {
		root.innerHTML = markup;
		for (const el of root.querySelectorAll<HTMLElement>("[data-line]")) {
			el.scrollIntoView = scroll;
			el.getBoundingClientRect = () => ({
				top: offscreen ? 5000 : 10,
				bottom: offscreen ? 5020 : 30,
				left: 0,
				right: 100,
				width: 100,
				height: 20,
				x: 0,
				y: 0,
				toJSON: () => ({}),
			});
		}
	};
	redraw(markup);
	const containerRef = { current: container };
	const hook = renderHook(({ selection, ready }) => useFileAnnotationReveal(containerRef, selection, {ready}), { initialProps: { selection: range, ready } });
	act(() => hook.result.current.reveal());
	return {
		...hook,
		scroll,
		root,
		redraw,
		cleanup: () => {
			hook.unmount();
			container.remove();
		},
	};
}
const range: FileAnnotationRange = { requestKey: 1, line: 2, endLine: 2, side: "file", lines: [{ kind: "context", oldNo: 2, newNo: 2, text: "captured();" }] };
describe("file annotation reveal", () => {
	it.each([false,true])("Retry continues loading without undoing a prior dismissal (dismissed=%s)", dismissed=>{
		const view=setup("",range,false,false);
		if(dismissed)fireEvent.keyDown(document,{key:"Escape"});
		const button=document.createElement("button");button.dataset.fileAnnotationRetry=String(range.requestKey);document.body.append(button);
		fireEvent.pointerDown(button,{button:0});
		document.querySelector("diffs-container")!.shadowRoot!.innerHTML='<div data-line="2" data-line-type="context">captured();</div>';
		view.rerender({selection:range,ready:true});act(()=>view.result.current.reveal());
		expect(Boolean(view.result.current.selectedLines)).toBe(!dismissed);
		view.cleanup();button.remove();
	});
	it.each([false,true])("refuses a continuous interval containing uncaptured code (split=%s)", split => {
		const selection:FileAnnotationRange={requestKey:1,side:"old",line:2,endLine:8,lines:[{kind:"del",side:"old",oldNo:2,newNo:null,text:"old();"},{kind:"add",side:"new",oldNo:null,newNo:8,text:"new();"}]};
		const markup=split
			? '<pre data-diff-type="split"><code data-deletions><div data-line="2" data-line-type="change-deletion" data-line-index="1,1">old();</div></code><code data-additions><div data-line="2" data-line-type="change-addition" data-line-index="2,1">unrelated();</div><div data-line="8" data-line-type="change-addition" data-line-index="8,7">new();</div></code></pre>'
			: '<pre data-diff-type="unified"><div data-line="2" data-line-type="change-deletion" data-line-index="1,1">old();</div><div data-line="2" data-line-type="change-addition" data-line-index="2,1">unrelated();</div><div data-line="8" data-line-type="change-addition" data-line-index="3,2">new();</div></pre>';
		const view=setup(markup,selection);
		expect(view.result.current.selectedLines).toBeNull();
		if (!split) expect(view.result.current.changed).toBe(true);
		view.cleanup();
	});
	it("highlights a visible verified range without scrolling", () => {
		const view = setup('<div data-line="2" data-line-type="context">captured();</div>', range);
		expect(view.result.current.selectedLines).toMatchObject({ start: 2, end: 2 });
		expect(view.scroll).not.toHaveBeenCalled();
		view.cleanup();
	});
	it("scrolls offscreen verified rows and processes a repeated request", () => {
		const view = setup('<div data-line="2" data-line-type="context">captured();</div>', range, true);
		expect(view.scroll).toHaveBeenCalledOnce();
		view.rerender({ selection: { ...range, requestKey: 2 }, ready:true });
		act(() => view.result.current.reveal());
		expect(view.scroll).toHaveBeenCalledTimes(2);
		view.cleanup();
	});
	it("refuses to highlight code changed between verification and rendering", () => {
		const view = setup('<div data-line="2" data-line-type="context">different();</div>', range, true);
		expect(view.result.current.changed).toBe(true);
		expect(view.result.current.selectedLines).toBeNull();
		expect(view.scroll).not.toHaveBeenCalled();
		view.cleanup();
	});
	it("keeps old-side line numbers in a unified diff and selects across both sides", () => {
		const view = setup(
			'<div data-line="2" data-line-type="change-deletion">removed();</div><div data-line="3" data-line-type="change-addition">added();</div>',
			{
				...range,
				side: "old",
				endLine: 3,
				lines: [
					{ kind: "del", oldNo: 2, newNo: null, text: "removed();" },
					{ kind: "add", oldNo: null, newNo: 3, text: "added();" },
				],
			},
		);
		expect(view.result.current.selectedLines).toEqual({ start: 2, end: 3, side: "deletions", endSide: "additions" });
		view.cleanup();
	});
});

it("clears an existing highlight when a live redraw replaces its code", () => {
	const view = setup('<div data-line="2" data-line-type="context">captured();</div>', range);
	expect(view.result.current.selectedLines).not.toBeNull();
	const host = document.querySelector("diffs-container")!;
	host.shadowRoot!.querySelector("[data-line]")!.textContent = "different();";
	act(() => view.result.current.reveal());
	expect(view.result.current.selectedLines).toBeNull();
	expect(view.result.current.changed).toBe(true);
	expect(view.scroll).not.toHaveBeenCalled();
	view.cleanup();
});

it("dismisses outside the rows and stays dismissed through redraws until reopened", () => {
	const view = setup('<div data-line="2" data-line-type="context">captured();</div>', range);
	const row = document.querySelector("diffs-container")!.shadowRoot!.querySelector("[data-line]")!;
	fireEvent.pointerDown(row, { button: 0, composed: true });
	expect(view.result.current.selectedLines).not.toBeNull();
	fireEvent.pointerDown(document.body, { button: 0 });
	expect(view.result.current.selectedLines).toBeNull();
	view.rerender({ selection: { ...range }, ready:true });
	act(() => view.result.current.reveal());
	expect(view.result.current.selectedLines).toBeNull();
	expect(view.result.current.changed).toBe(false);
	view.rerender({ selection: { ...range, requestKey: 2 }, ready:true });
	act(() => view.result.current.reveal());
	expect(view.result.current.selectedLines).toMatchObject({ start: 2, end: 2 });
	view.cleanup();
});

it("dismisses with Escape without clearing unrelated text selection or marking the source changed", () => {
	const view = setup('<div data-line="2" data-line-type="context">captured();</div>', range);
	const other = document.createElement("p");
	other.textContent = "keep my selected text";
	document.body.append(other);
	const selection = window.getSelection()!;
	const textRange = document.createRange();
	textRange.selectNodeContents(other);
	selection.removeAllRanges();
	selection.addRange(textRange);
	fireEvent.keyDown(document, { key: "Escape" });
	act(() => view.result.current.reveal());
	expect(view.result.current.selectedLines).toBeNull();
	expect(view.result.current.changed).toBe(false);
	expect(selection.toString()).toBe("keep my selected text");
	selection.removeAllRanges();
	other.remove();
	view.cleanup();
});

it("distinguishes old and new shadow-root rows when dismissing a split diff", () => {
	const old = { ...range, side: "old" as const, lines: [{ kind: "del" as const, side: "old" as const, oldNo: 2, newNo: null, text: "captured();" }] };
	const view = setup('<div data-deletions><div data-line="2" data-line-type="change-deletion">captured();</div></div><div data-line="2" data-line-type="change-addition">replacement();</div>', old);
	const root = document.querySelector("diffs-container")!.shadowRoot!;
	fireEvent.pointerDown(root.querySelector("[data-deletions] [data-line]")!, { button: 0, composed: true });
	expect(view.result.current.selectedLines).not.toBeNull();
	fireEvent.pointerDown(root.querySelector('[data-line-type="change-addition"]')!, { button: 0, composed: true });
	expect(view.result.current.selectedLines).toBeNull();
	view.cleanup();
});

it.each(["\n","\r\n"])("verifies a range containing rendered blank/whitespace rows (%j)", ending => {
 const selection: FileAnnotationRange={...range,line:1,endLine:3,lines:[{kind:"context",oldNo:1,newNo:1,text:"services:"},{kind:"context",oldNo:2,newNo:2,text:""},{kind:"context",oldNo:3,newNo:3,text:" \t"}]};
 const view=setup('<div data-line="1" data-line-type="context">services:</div><div data-line="2" data-line-type="context"></div><div data-line="3" data-line-type="context"> \t</div>',selection);
 const rows=document.querySelector("diffs-container")!.shadowRoot!.querySelectorAll<HTMLElement>("[data-line]");
 rows[1].textContent=ending;rows[2].textContent=" \t"+ending;
 act(()=>view.result.current.reveal());
 expect(view.result.current.changed).toBe(false);
 expect(view.result.current.selectedLines).toMatchObject({start:1,end:3});
 rows[2].textContent="  ";act(()=>view.result.current.reveal());
 expect(view.result.current.changed).toBe(true);
 expect(view.result.current.selectedLines).toBeNull();
 view.cleanup();
});


it("clears a timeout warning when delayed rows finally verify", () => {
 vi.useFakeTimers();
 const view=setup("",range);
 act(()=>vi.advanceTimersByTime(5001));
 expect(view.result.current.changed).toBe(false);
 expect(view.result.current.unverified).toBe(true);
 const root=document.querySelector("diffs-container")!.shadowRoot!;
 root.innerHTML='<div data-line="2" data-line-type="context">captured();</div>';
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toMatchObject({start:2,end:2});
 expect(view.result.current.changed).toBe(false);
 view.cleanup();vi.useRealTimers();
});
it("waits for refreshed content and recovers on the first moved-range request",()=>{
 const view=setup('<div data-line="2" data-line-type="context">cached old row</div>',range,false,false);
 expect(view.result.current.changed).toBe(false);
 const root=document.querySelector("diffs-container")!.shadowRoot!;
 root.querySelector("[data-line]")!.textContent="captured();";
 view.rerender({selection:range,ready:true});
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toMatchObject({start:2,end:2});
 expect(view.result.current.changed).toBe(false);
 view.cleanup();
});
it("can verify a corrected first render without reopening",()=>{
 const view=setup('<div data-line="2" data-line-type="context">old source</div>',range);
 expect(view.result.current.changed).toBe(true);
 document.querySelector("diffs-container")!.shadowRoot!.querySelector("[data-line]")!.textContent="captured();";
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toMatchObject({start:2,end:2});
 expect(view.result.current.changed).toBe(false);
 view.cleanup();
});
it("keeps a slow request dismissed even after its rows become available",()=>{
 const view=setup("",range,false,false);
 fireEvent.keyDown(document,{key:"Escape"});
 document.querySelector("diffs-container")!.shadowRoot!.innerHTML='<div data-line="2" data-line-type="context">captured();</div>';
 view.rerender({selection:range,ready:true});act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toBeNull();
 view.cleanup();
});


it.each(["plain", "unified", "split"])("recovers a consumed %s highlight after a redraw without scrolling again", mode => {
 const diff = mode !== "plain";
 const selection: FileAnnotationRange = diff ? {...range, side:"old", lines:[
  {kind:"del",side:"old",oldNo:2,newNo:null,text:"old();"},
  {kind:"add",side:"new",oldNo:null,newNo:2,text:"new();"},
 ]} : range;
 const markup = diff
  ? `<pre data-diff-type="${mode}"><div data-line="2" data-line-type="change-deletion" data-line-index="1,1">old();</div><div data-line="2" data-line-type="change-addition" data-line-index="2,1">new();</div></pre>`
  : '<div data-line="2" data-line-type="context">captured();</div>';
 const view = setup(markup, selection, true);
 const verified = view.result.current.selectedLines;
 expect(verified).not.toBeNull();
 expect(view.scroll).toHaveBeenCalledOnce();
 view.redraw(markup.replace(diff ? "new();" : "captured();", "changed();"));
 act(() => view.result.current.reveal());
 expect(view.result.current.selectedLines).toBeNull();
 expect(view.result.current.changed).toBe(true);
 view.redraw(markup);
 act(() => view.result.current.reveal());
 expect(view.result.current.selectedLines).toEqual(verified);
 expect(view.result.current.changed).toBe(false);
 const recovered = view.result.current.selectedLines;
 act(() => { for(let i=0;i<5;i++) view.result.current.reveal(); });
 expect(view.result.current.selectedLines).toBe(recovered);
 expect(view.scroll).toHaveBeenCalledOnce();
 fireEvent.pointerDown(view.root.querySelector('[data-line]')!, {button:0,composed:true});
 expect(view.result.current.selectedLines).toBe(recovered);
 view.cleanup();
});

it.each(["Escape", "outside"])("keeps %s dismissal effective during mismatch recovery until a new request", method => {
 const markup='<div data-line="2" data-line-type="context">captured();</div>';
 const view=setup(markup,range,true);
 view.redraw(markup.replace("captured();","changed();"));
 act(()=>view.result.current.reveal());
 expect(view.result.current.changed).toBe(true);
 if(method==="Escape")fireEvent.keyDown(document,{key:"Escape"});
 else fireEvent.pointerDown(document.body,{button:0});
 view.redraw(markup);
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toBeNull();
 expect(view.result.current.changed).toBe(true);
 expect(view.scroll).toHaveBeenCalledOnce();
 view.rerender({selection:{...range,requestKey:2},ready:true});
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).not.toBeNull();
 expect(view.result.current.changed).toBe(false);
 expect(view.scroll).toHaveBeenCalledTimes(2);
 view.cleanup();
});

it("waits for readiness and every captured row before recovering a consumed request",()=>{
 const selection:FileAnnotationRange={...range,line:1,endLine:3,lines:[1,2,3].map(n=>({kind:"context",oldNo:n,newNo:n,text:`row${n};`}))};
 const row=(n:number)=>`<div data-line="${n}" data-line-type="context" data-line-index="${n},${n}">row${n};</div>`;
 const markup=[1,2,3].map(row).join("");
 const view=setup(markup,selection,true);
 view.redraw(markup.replace("row2;","changed();"));act(()=>view.result.current.reveal());
 expect(view.result.current.changed).toBe(true);
 view.rerender({selection,ready:false});view.redraw(markup);act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toBeNull();
 expect(view.result.current.changed).toBe(false);
 view.redraw(row(1)+row(3));view.rerender({selection,ready:true});act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toBeNull();
 expect(view.result.current.changed).toBe(false);
 view.redraw(markup);act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toMatchObject({start:1,end:3});
 expect(view.result.current.changed).toBe(false);
 expect(view.scroll).toHaveBeenCalledOnce();
 view.cleanup();
});

it("does not restore a consumed interval with matching endpoints and extra rows",()=>{
 const selection:FileAnnotationRange={...range,side:"old",lines:[{kind:"del",side:"old",oldNo:2,newNo:null,text:"old();"},{kind:"add",side:"new",oldNo:null,newNo:2,text:"new();"}]};
 const first='<div data-line="2" data-line-type="change-deletion">old();</div>';
 const last='<div data-line="2" data-line-type="change-addition">new();</div>';
 const view=setup(first+last,selection,true);
 view.redraw(first+'<div data-line="3" data-line-type="context">unrelated();</div>'+last);
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toBeNull();
 expect(view.result.current.changed).toBe(true);
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toBeNull();
 expect(view.result.current.changed).toBe(true);
 view.redraw(first+last);act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).not.toBeNull();
 expect(view.result.current.changed).toBe(false);
 expect(view.scroll).toHaveBeenCalledOnce();
 view.cleanup();
});

it("ignores an older request's render callback after opening another annotation",()=>{
 const markup='<div data-line="2" data-line-type="context">captured();</div>';
 const view=setup(markup,range,true);
 const staleReveal=view.result.current.reveal;
 view.rerender({selection:{...range,requestKey:2},ready:true});act(()=>view.result.current.reveal());
 const currentSelection=view.result.current.selectedLines;
 view.redraw(markup.replace("captured();","changed();"));
 act(()=>staleReveal());
 expect(view.result.current.selectedLines).toBe(currentSelection);
 expect(view.result.current.changed).toBe(false);
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).toBeNull();
 expect(view.result.current.changed).toBe(true);
 view.cleanup();
});


it("starts the render deadline only after a slow source becomes ready", () => {
 vi.useFakeTimers();
 const view=setup("",range,false,false);
 act(()=>vi.advanceTimersByTime(30000));
 expect(view.result.current).toMatchObject({loading:true,changed:false,unverified:false,selectedLines:null});
 view.rerender({selection:range,ready:true});
 act(()=>vi.advanceTimersByTime(4999));
 expect(view.result.current.unverified).toBe(false);
 act(()=>vi.advanceTimersByTime(2));
 expect(view.result.current).toMatchObject({loading:false,changed:false,unverified:true,selectedLines:null});
 view.redraw('<div data-line="2" data-line-type="context">captured();</div>');
 act(()=>view.result.current.reveal());
 expect(view.result.current.selectedLines).not.toBeNull();
 expect(view.result.current.unverified).toBe(false);
 view.cleanup();vi.useRealTimers();
});
it("cancels the old render deadline when readiness or navigation changes", () => {
 vi.useFakeTimers();
 const view=setup("",range);
 act(()=>vi.advanceTimersByTime(4000));
 view.rerender({selection:{...range,requestKey:2},ready:false});
 act(()=>vi.advanceTimersByTime(10000));
 expect(view.result.current).toMatchObject({loading:true,changed:false,unverified:false});
 fireEvent.keyDown(document,{key:"Escape"});
 view.rerender({selection:{...range,requestKey:2},ready:true});
 act(()=>vi.advanceTimersByTime(10000));
 expect(view.result.current).toMatchObject({loading:false,changed:false,unverified:false});
 view.cleanup();vi.useRealTimers();
});
