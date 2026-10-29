import { render, screen, waitFor } from "@testing-library/react";
import { useRef } from "react";
import { describe, expect, it } from "vitest";
import { parsePatchFiles } from "@pierre/diffs";
import { FileDiff } from "@pierre/diffs/react";
import type { FileAnnotationRange } from "../../../shared/chat-file-reference";
import { useFileAnnotationReveal } from "./useFileAnnotationReveal";

const patch="diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n@@ -1,4 +1,4 @@\n keep\n-old();\n+unrelated();\n middle\n end\n@@ -7,2 +7,2 @@\n tail\n-other();\n+new();\n";
function Viewer({split,selection,source=patch}:{split:boolean;selection:FileAnnotationRange;source?:string}) {
	const ref=useRef<HTMLDivElement>(null);
	const reveal=useFileAnnotationReveal(ref,selection);
	return <div ref={ref}><FileDiff disableWorkerPool fileDiff={parsePatchFiles(source)[0].files[0]} selectedLines={reveal.selectedLines} options={{diffStyle:split?"split":"unified",disableFileHeader:true,onPostRender:reveal.reveal}}/><output data-testid="verification">{reveal.changed?"mismatch":reveal.selectedLines?"highlighted":"pending"}</output></div>;
}
describe("real Pierre annotation intervals",()=>{
	it.each([false,true])("does not highlight intervening unrelated rows (split=%s)",async split=>{
		const selection:FileAnnotationRange={requestKey:1,side:"old",line:2,endLine:8,lines:[{kind:"del",side:"old",oldNo:2,newNo:null,text:"old();"},{kind:"add",side:"new",oldNo:null,newNo:8,text:"new();"}]};
		render(<Viewer split={split} selection={selection}/>);
		await waitFor(()=>expect(screen.getByTestId("verification")).toHaveTextContent("mismatch"));
		const root=document.querySelector("diffs-container")!.shadowRoot!;
		expect(root.querySelector("[data-line][data-selected-line]")).toBeNull();
	});
	it.each([false,true])("highlights a complete matching two-row replacement (split=%s)",async split=>{
		const source="diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n@@ -1,4 +1,4 @@\n keep\n-old1();\n-old2();\n+new1();\n+new2();\n end\n";
		const selection:FileAnnotationRange={requestKey:1,side:"old",line:2,endLine:3,lines:[{kind:"del",side:"old",oldNo:2,newNo:null,text:"old1();"},{kind:"del",side:"old",oldNo:3,newNo:null,text:"old2();"},{kind:"add",side:"new",oldNo:null,newNo:2,text:"new1();"},{kind:"add",side:"new",oldNo:null,newNo:3,text:"new2();"}]};
		render(<Viewer source={source} split={split} selection={selection}/>);
		await waitFor(()=>expect(screen.getByTestId("verification")).toHaveTextContent("highlighted"));
	});
});


it.each([false,true])("restores the actual Pierre highlight after mismatch recovery without reopening (split=%s)",async split=>{
 const source="diff --git a/a.ts b/a.ts\n--- a/a.ts\n+++ b/a.ts\n@@ -1,3 +1,3 @@\n keep\n-old();\n+new();\n end\n";
 const selection:FileAnnotationRange={requestKey:1,side:"old",line:2,endLine:2,lines:[{kind:"del",side:"old",oldNo:2,newNo:null,text:"old();"},{kind:"add",side:"new",oldNo:null,newNo:2,text:"new();"}]};
 const view=render(<Viewer source={source} split={split} selection={selection}/>);
 await waitFor(()=>expect(screen.getByTestId("verification")).toHaveTextContent("highlighted"));
 const highlighted=()=>document.querySelector("diffs-container")!.shadowRoot!.querySelectorAll("[data-line][data-selected-line]");
 await waitFor(()=>expect(highlighted().length).toBeGreaterThan(0));
 view.rerender(<Viewer source={source.replace("new();","changed();")} split={split} selection={selection}/>);
 await waitFor(()=>expect(screen.getByTestId("verification")).toHaveTextContent("mismatch"));
 await waitFor(()=>expect(highlighted()).toHaveLength(0));
 view.rerender(<Viewer source={source} split={split} selection={selection}/>);
 await waitFor(()=>expect(screen.getByTestId("verification")).toHaveTextContent("highlighted"));
 await waitFor(()=>expect(highlighted().length).toBeGreaterThan(0));
});
