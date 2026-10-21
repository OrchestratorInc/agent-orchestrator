package cua

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// AX hit testing follows pointer routing, including click-through overlays.
// The window-number bridge keeps admission tied to the exact owned CG window.
const axHitTestScript = `ObjC.import("ApplicationServices"); ObjC.import("AppKit");
function run(argv) {
 var answer={trusted:Boolean($.AXIsProcessTrusted()),ancestry:[]};
 try {
  if(!answer.trusted)throw Error("Accessibility permission is unavailable");
  ObjC.bindFunction("_AXUIElementGetWindow", ["int", ["void *", "unsigned int *"]]);
  function attr(e,name){var v=Ref();var c=$.AXUIElementCopyAttributeValue(e,$(name),v);return {code:c,value:c===0?v[0]:null};}
  function text(e,name){var a=attr(e,name);return a.code===0?String(ObjC.unwrap(ObjC.castRefToObject(a.value))):"";}
  var hit=Ref();answer.hitCode=$.AXUIElementCopyElementAtPosition($.AXUIElementCreateSystemWide(),Number(argv[0]),Number(argv[1]),hit);
  if(answer.hitCode!==0)throw Error("AXUIElementCopyElementAtPosition failed: "+answer.hitCode);
  var node=hit[0],rawNode=hit[0];
  for(var i=0;i<64;i++) {
   var pid=Ref();var code=$.AXUIElementGetPid(node,pid);
   if(code!==0)throw Error("AXUIElementGetPid failed: "+code);
   var role=text(node,"AXRole"),app=$.NSRunningApplication.runningApplicationWithProcessIdentifier(pid[0]);
   answer.ancestry.push({pid:pid[0],role:role,title:text(node,"AXTitle"),owner:app?ObjC.unwrap(app.localizedName):""});
   if(role==="AXWindow"){
    var wid=Ref();answer.windowCode=$._AXUIElementGetWindow(rawNode,wid);answer.windowID=wid[0];
    if(answer.windowCode!==0)throw Error("AX window number failed: "+answer.windowCode);
    return JSON.stringify(answer);
   }
   var parent=attr(node,"AXParent");
   if(parent.code!==0)throw Error("AXParent failed: "+parent.code);
   if($.CFEqual(node,ObjC.castRefToObject(parent.value)))throw Error("AX ancestry cycle");
   rawNode=parent.value;node=ObjC.castRefToObject(parent.value);
  }
  throw Error("AX ancestry did not reach a window within 64 parents");
 }catch(e){answer.error=String(e);return JSON.stringify(answer);}
}`

type axHitNode struct {
	PID   int    `json:"pid"`
	Role  string `json:"role"`
	Title string `json:"title"`
	Owner string `json:"owner"`
}

type axHitTest struct {
	Trusted    *bool       `json:"trusted"`
	HitCode    *int        `json:"hitCode"`
	WindowCode *int        `json:"windowCode"`
	WindowID   int         `json:"windowID"`
	Ancestry   []axHitNode `json:"ancestry"`
	Error      string      `json:"error"`
}

func (a *Adapter) checkAXClickPoint(ctx context.Context, b *binding, r *captureReceipt, point pixel) error {
	// Cua's foreground pointer route truncates to integer global screen points.
	x := int(r.frame.Bounds.X + float64(point.x)/r.frame.Scale)
	y := int(r.frame.Bounds.Y + float64(point.y)/r.frame.Scale)
	out, err := a.run(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", axHitTestScript, strconv.Itoa(x), strconv.Itoa(y))
	if err != nil || ctx.Err() != nil {
		return &Error{Code: "window_hit_test_unknown", Detail: providerCallDiagnostic(ctx, "AX system-wide hit test", nil, string(out.Stderr), err, nil), cause: errors.Join(ErrRefused, err, ctx.Err())}
	}
	var hit axHitTest
	if err := json.Unmarshal(out.Stdout, &hit); err != nil {
		return &Error{Code: "window_hit_test_unknown", Detail: providerCallDiagnostic(ctx, "AX system-wide hit test", nil, "invalid AX hit-test result", nil, err), cause: errors.Join(ErrRefused, err)}
	}
	detail, err := hit.ownedPoint(b.target, x, y)
	if err != nil {
		return err
	}
	if r.inputDetail != "" {
		r.inputDetail += "; "
	}
	r.inputDetail += detail
	return nil
}

func (hit axHitTest) ownedPoint(target domain.TestTargetIdentity, x, y int) (string, error) {
	if hit.Error != "" {
		return "", refuse("window_hit_test_unknown", providerDiagnosticText(hit.Error, nil, providerDiagnosticLimit))
	}
	if hit.Trusted == nil || !*hit.Trusted || hit.HitCode == nil || *hit.HitCode != 0 || hit.WindowCode == nil || *hit.WindowCode != 0 || hit.WindowID <= 0 || len(hit.Ancestry) == 0 || len(hit.Ancestry) > 64 || hit.Ancestry[len(hit.Ancestry)-1].Role != "AXWindow" {
		return "", refuse("window_hit_test_unknown", "AX hit test did not prove permission, successful routing and window ancestry")
	}
	node := hit.Ancestry[0]
	detail := fmt.Sprintf("AX hit at (%d,%d): owner=%q pid=%d window=%d role=%q title=%q", x, y, providerDiagnosticText(node.Owner, nil, 128), node.PID, hit.WindowID, node.Role, providerDiagnosticText(node.Title, nil, 256))
	roles := make([]string, 0, len(hit.Ancestry))
	for _, ancestor := range hit.Ancestry {
		if ancestor.PID <= 0 || ancestor.Role == "" {
			return "", refuse("window_hit_test_unknown", "AX ancestry lacks PID or role")
		}
		if ancestor.PID != target.ElectronPID {
			return "", refuse("window_point_not_owned", fmt.Sprintf("%s; foreign ancestor owner=%q pid=%d role=%q", detail, providerDiagnosticText(ancestor.Owner, nil, 128), ancestor.PID, ancestor.Role))
		}
		roles = append(roles, providerDiagnosticText(ancestor.Role, nil, 64))
	}
	if strconv.Itoa(hit.WindowID) != target.WindowID {
		return "", refuse("window_point_not_owned", detail+"; expected owned window "+target.WindowID)
	}
	return detail + "; ancestry=" + strings.Join(roles, " -> "), nil
}
