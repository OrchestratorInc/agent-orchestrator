// Shared by the renderer and the sandbox bootstrap. The host supplies the
// session find Unicode fold table; no document text crosses the frame boundary.
export function createArtifactSearchEngine(exceptions) {
  var owned;
  function fold(source) {
    if (/^[\x00-\x7F]*$/.test(source)) {
      var ascii = source.toLowerCase().replace(/[\t-\r]/g, " ");
      if (!/ {2}/.test(ascii)) return { text: ascii, starts: [], ends: [], identity: true };
    }
    var text = "", starts = [], ends = [];
    for (var item of new Intl.Segmenter("und", { granularity: "grapheme" }).segment(source)) {
      var value = Array.from(item.segment.normalize("NFD"), function (c) { return exceptions[c] || c.toLowerCase(); }).join("");
      for (var c of value) {
        var next = /[\p{White_Space}\uFEFF]/u.test(c) ? " " : c;
        if (next === " " && text.endsWith(" ")) { ends[ends.length - 1] = item.index + item.segment.length; continue; }
        text += next;
        for (var i = 0; i < next.length; i++) { starts.push(item.index); ends.push(item.index + item.segment.length); }
      }
    }
    return { text: text, starts: starts, ends: ends };
  }
  function occurrences(source, query) {
    var needle = fold(query).text.trim(), found = [];
    if (!needle) return found;
    var folded = fold(source);
    for (var from = 0; from < folded.text.length;) {
      var start = folded.text.indexOf(needle, from);
      if (start < 0) break;
      var end = start + needle.length;
      if (/^\p{M}/u.test(folded.text.slice(end))) { from = end; continue; }
      found.push({ start: folded.identity ? start : folded.starts[start], end: folded.identity ? end : folded.ends[end - 1] });
      from = end;
      while (!folded.identity && from < folded.text.length && folded.starts[from] < folded.ends[end - 1]) from++;
    }
    return found;
  }
  var excluded = "script,style,noscript,template,iframe,input,textarea,select,svg title,svg desc,svg defs,[contenteditable='true'],[hidden],[aria-hidden='true'],[data-artifact-find-exclude],[data-excerpt-exclude],[data-selection-toolbar-header],[data-line-annotation],[data-annotation-slot],[data-column-number],[data-gutter-utility-slot],[role='search'],[role='status'],[role='alert'],.cm-editor,[data-editing='true']";
  function blocks(root) {
    var found = [], current;
    function visit(node, boundary) {
      if (node.nodeType === 3) {
        if (!node.data) return;
        if (!current || current.element !== boundary) { current = { element: boundary, text: "", nodes: [] }; found.push(current); }
        current.text += node.data; current.nodes.push(node); return;
      }
      if (node.nodeType !== 1 && node.nodeType !== 11) return;
      if (node.nodeType === 1) {
        if (node.matches(excluded) || (node.matches("button,[role='button']") && !node.closest("th"))) { current = undefined; return; }
        var style = node.ownerDocument.defaultView.getComputedStyle(node);
        if (style.display === "none" || style.visibility === "hidden" || style.visibility === "collapse" || style.opacity === "0" || style.contentVisibility === "hidden") { current = undefined; return; }
        var flowingText = boundary.nodeType === 1 && boundary.matches(".pdf-text-layer,[data-content]");
        if (!flowingText && node.matches("p,h1,h2,h3,h4,h5,h6,td,th,li,pre,[data-content],[data-line],text,.pdf-text-layer")) boundary = node;
        else if (!flowingText && (style.display === "block" || style.display === "flex" || style.display === "grid")) boundary = node;
        if (node.tagName === "BR") {
          if (!current || current.element !== boundary) { current = { element: boundary, text: "", nodes: [] }; found.push(current); }
          current.text += "\n"; current.nodes.push(node.ownerDocument.createTextNode("\n")); return;
        }
        if (node.shadowRoot) { visit(node.shadowRoot, node); return; }
        // Closed details expose their summary, not the concealed body.
        if (node.tagName === "DETAILS" && !node.open) { var summary = node.querySelector("summary"); if (summary) visit(summary, summary); current = undefined; return; }
      }
      for (var child of node.childNodes) visit(child, boundary);
    }
    visit(root, root);
    return found.filter(function (block) { return block.text.trim(); });
  }
  function range(nodes, start, end) {
    var result = nodes[0] && nodes[0].ownerDocument.createRange(), offset = 0, begun = false;
    if (!result) return;
    for (var node of nodes) {
      var next = offset + node.length;
      if (!begun && start < next) { result.setStart(node, start - offset); begun = true; }
      if (begun && end <= next) { result.setEnd(node, end - offset); return result; }
      offset = next;
    }
  }
  function ranges(root, query) {
    return blocks(root).flatMap(function (block) {
      return occurrences(block.text, query).map(function (hit) { return range(block.nodes, hit.start, hit.end); }).filter(Boolean);
    });
  }
  function anchor(match) {
    if (!match) return;
    return { startContainer: match.startContainer, startOffset: match.startOffset, endContainer: match.endContainer, endOffset: match.endOffset, startText: match.startContainer.textContent, endText: match.endContainer.textContent, row: match.startContainer.parentElement?.closest("tr")?.textContent };
  }
  function retained(matches, previous) {
    if (!previous) return -1;
    var same = function (match) { return match.startOffset === previous.startOffset && match.endOffset === previous.endOffset && match.startContainer.textContent === previous.startText && match.endContainer.textContent === previous.endText && match.startContainer.parentElement?.closest("tr")?.textContent === previous.row; };
    var exact = matches.findIndex(function (match) { return match.startContainer === previous.startContainer && match.endContainer === previous.endContainer && same(match); });
    return exact >= 0 ? exact : matches.findIndex(same);
  }
  // Match chat-find.css, including high-contrast OS themes.
  var highlightCSS = "::highlight(artifact-find-matches){background-color:#ffe066;color:#202020}::highlight(artifact-find-active){background-color:#c4580e;color:#fff;text-decoration:underline}@media(forced-colors:active){::highlight(artifact-find-matches){background-color:Mark;color:MarkText}::highlight(artifact-find-active){background-color:Highlight;color:HighlightText}}";
  function clear() {
    if (!owned) return;
    if (owned.registry.get("artifact-find-matches") === owned.all) owned.registry.delete("artifact-find-matches");
    if (owned.registry.get("artifact-find-active") === owned.active) owned.registry.delete("artifact-find-active");
    owned = undefined;
  }
  function paint(matches, active, doc) {
    clear();
    var win = doc.defaultView, registry = win.CSS && win.CSS.highlights;
    if (!registry || !win.Highlight) return;
    var all = new win.Highlight(), selected = new win.Highlight();
    for (var match of matches) {
      all.add(match);
      var root = match.startContainer.getRootNode(), target = root.nodeType === 9 ? root.head : root;
      if (target && !target.querySelector("style[data-artifact-find-style]")) {
        var style = doc.createElement("style"); style.dataset.artifactFindStyle = ""; style.textContent = highlightCSS; target.append(style);
      }
    }
    if (matches[active]) selected.add(matches[active]);
    registry.set("artifact-find-matches", all); registry.set("artifact-find-active", selected);
    owned = { registry: registry, all: all, active: selected };
  }
  function reveal(match) {
    if (!match) return;
    var element = match.startContainer.parentElement;
    if (!element) return;
    element.scrollIntoView({ block: "center", inline: "nearest" });
    // A single pre or table cell may span several viewports in either direction.
    var rect = match.getBoundingClientRect();
    for (var parent = element; parent; parent = parent.parentElement || parent.getRootNode().host) {
      var bounds = parent.getBoundingClientRect(), css = parent.ownerDocument.defaultView.getComputedStyle(parent);
      if (/(auto|scroll)/.test(css.overflowY) && (rect.top < bounds.top || rect.bottom > bounds.bottom)) parent.scrollTop += rect.top + rect.height / 2 - bounds.top - bounds.height / 2;
      if (/(auto|scroll)/.test(css.overflowX) && (rect.left < bounds.left || rect.right > bounds.right)) parent.scrollLeft += rect.left + rect.width / 2 - bounds.left - bounds.width / 2;
    }
  }
  return { occurrences: occurrences, blocks: blocks, range: range, ranges: ranges, anchor: anchor, retained: retained, paint: paint, clear: clear, reveal: reveal };
}
