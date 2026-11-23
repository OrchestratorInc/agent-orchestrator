// Shared by the renderer and the sandbox bootstrap. The host supplies the
// session find Unicode fold table; no document text crosses the frame boundary.
export function createArtifactSearchEngine(exceptions) {
  var owned;
  function sync(iterator) {
    var step;
    do { step = iterator.next(); } while (!step.done);
    return step.value;
  }
  async function cooperate(iterator, signal) {
    var deadline = performance.now() + 8, step;
    do {
      if (signal?.aborted) throw new DOMException("Cancelled", "AbortError");
      step = iterator.next();
      if (!step.done && performance.now() >= deadline) {
        await new Promise(function (resolve) { setTimeout(resolve, 0); });
        deadline = performance.now() + 8;
      }
    } while (!step.done);
    if (signal?.aborted) throw new DOMException("Cancelled", "AbortError");
    return step.value;
  }
  function* fold(source) {
    if (/^[\x00-\x7F]*$/.test(source)) {
      var ascii = source.toLowerCase().replace(/[\t-\r]/g, " ");
      if (!/ {2}/.test(ascii)) return { text: ascii, starts: [], ends: [], identity: true };
    }
    var text = "", starts = [], ends = [], count = 0;
    for (var item of new Intl.Segmenter("und", { granularity: "grapheme" }).segment(source)) {
      var value = Array.from(item.segment.normalize("NFD"), function (c) { return exceptions[c] || c.toLowerCase(); }).join("");
      for (var c of value) {
        var next = /[\p{White_Space}\uFEFF]/u.test(c) ? " " : c;
        if (next === " " && text.endsWith(" ")) { ends[ends.length - 1] = item.index + item.segment.length; continue; }
        text += next;
        for (var i = 0; i < next.length; i++) { starts.push(item.index); ends.push(item.index + item.segment.length); }
      }
      if (++count % 128 === 0) yield;
    }
    return { text: text, starts: starts, ends: ends };
  }
  function* occurrenceSteps(source, query) {
    var needle = (yield* fold(query)).text.trim(), found = [], count = 0;
    if (!needle) return found;
    var folded = yield* fold(source);
    for (var from = 0; from < folded.text.length;) {
      var start = folded.text.indexOf(needle, from);
      if (start < 0) break;
      var end = start + needle.length;
      if (/^\p{M}/u.test(folded.text.slice(end))) { from = end; continue; }
      found.push({ start: folded.identity ? start : folded.starts[start], end: folded.identity ? end : folded.ends[end - 1] });
      from = end;
      while (!folded.identity && from < folded.text.length && folded.starts[from] < folded.ends[end - 1]) from++;
      if (++count % 128 === 0) yield;
    }
    return found;
  }
  var excluded = "script,style,noscript,template,iframe,input,textarea,select,svg title,svg desc,svg defs,[contenteditable]:not([contenteditable='false']),[hidden],[aria-hidden='true'],[data-artifact-find-exclude],[data-excerpt-exclude],[data-selection-toolbar-header],[data-line-annotation],[data-annotation-slot],[data-column-number],[data-gutter-utility-slot],[role='search'],[role='status'],[role='alert'],.cm-editor,.monaco-editor,.xterm,.terminal-surface,[data-editing='true']";
  function children(node) {
    if (node.nodeType === 1 && node.localName === "slot") {
      var assigned = node.assignedNodes({ flatten: true });
      if (assigned.length) return assigned;
    }
    return node.shadowRoot ? [node.shadowRoot] : node.childNodes;
  }
  function* blockSteps(root) {
    var found = [], current, stack = [[root, root]], seen = new Set(), count = 0;
    while (stack.length) {
      var entry = stack.pop(), node = entry[0], boundary = entry[1];
      if (seen.has(node)) continue;
      seen.add(node);
      if (++count % 128 === 0) yield;
      if (node.nodeType === 3) {
        if (!node.data) continue;
        if (!current || current.element !== boundary) { current = { element: boundary, text: "", nodes: [] }; found.push(current); }
        current.text += node.data; current.nodes.push(node); continue;
      }
      if (node.nodeType !== 1 && node.nodeType !== 11) continue;
      if (node.nodeType === 1) {
        if (node.matches(excluded) || (node.matches("button,[role='button']") && !node.closest("th"))) { current = undefined; continue; }
        var style = node.ownerDocument.defaultView.getComputedStyle(node);
        if (style.display === "none" || style.visibility === "hidden" || style.visibility === "collapse" || style.opacity === "0" || style.contentVisibility === "hidden") { current = undefined; continue; }
        var flowingText = boundary.nodeType === 1 && boundary.matches(".pdf-text-layer,[data-content]");
        if (!flowingText && node.matches("p,h1,h2,h3,h4,h5,h6,td,th,li,pre,[data-content],[data-line],text,.pdf-text-layer")) boundary = node;
        else if (!flowingText && (style.display === "block" || style.display === "flex" || style.display === "grid")) boundary = node;
        if (node.tagName === "BR") {
          if (!current || current.element !== boundary) { current = { element: boundary, text: "", nodes: [] }; found.push(current); }
          current.text += "\n"; current.nodes.push(node.ownerDocument.createTextNode("\n")); continue;
        }
        // Closed details expose their summary, not the concealed body.
        if (node.tagName === "DETAILS" && !node.open) { var summary = node.querySelector("summary"); if (summary) stack.push([summary, summary]); current = undefined; continue; }
      }
      var nodes = children(node);
      for (var i = nodes.length - 1; i >= 0; i--) stack.push([nodes[i], boundary]);
    }
    return found.filter(function (block) { return block.text.trim(); });
  }
  // Both lists are ordered: advance the node cursor once, rather than
  // rescanning the block for every hit. One logical hit may cross DOM roots.
  function* mapHits(nodes, hits) {
    var matches = [], cursor = 0, offset = 0, count = 0;
    for (var hit of hits) {
      while (cursor < nodes.length && offset + nodes[cursor].length <= hit.start) {
        offset += nodes[cursor++].length;
        if (++count % 128 === 0) yield;
      }
      var parts = [], index = cursor, position = offset;
      while (index < nodes.length && position < hit.end) {
        var node = nodes[index], length = node.length;
        if (node.parentNode && length) {
          var start = Math.max(0, hit.start - position), end = Math.min(length, hit.end - position), previous = parts.at(-1);
          if (end > start) {
            if (previous && previous.endContainer.getRootNode() === node.getRootNode() &&
                (previous.endContainer === node || previous.endContainer.compareDocumentPosition(node) & 4)) previous.setEnd(node, end);
            else { var part = node.ownerDocument.createRange(); part.setStart(node, start); part.setEnd(node, end); parts.push(part); }
          }
        }
        position += length; index++;
        if (++count % 128 === 0) yield;
      }
      if (parts.length) matches.push(parts);
    }
    return matches;
  }
  function* searchSteps(root, query) {
    var matches = [], blocks = yield* blockSteps(root);
    for (var block of blocks) {
      var hits = yield* occurrenceSteps(block.text, query);
      var mapped = yield* mapHits(block.nodes, hits);
      for (var match of mapped) { matches.push(match); yield; }
    }
    return matches;
  }
  function parts(match) { return Array.isArray(match) ? match : match ? [match] : []; }
  function first(match) { return parts(match)[0]; }
  function anchor(match) {
    match = first(match);
    if (!match) return;
    return { startContainer: match.startContainer, startOffset: match.startOffset, endContainer: match.endContainer, endOffset: match.endOffset, startText: match.startContainer.textContent, endText: match.endContainer.textContent, row: match.startContainer.parentElement?.closest("tr")?.textContent };
  }
  function retained(matches, previous) {
    if (!previous) return -1;
    var same = function (value) { var match = first(value); return match.startOffset === previous.startOffset && match.endOffset === previous.endOffset && match.startContainer.textContent === previous.startText && match.endContainer.textContent === previous.endText && match.startContainer.parentElement?.closest("tr")?.textContent === previous.row; };
    var exact = matches.findIndex(function (value) { var match = first(value); return match.startContainer === previous.startContainer && match.endContainer === previous.endContainer && same(value); });
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
    var all = new win.Highlight(), selected = new win.Highlight(), styled = new Set();
    for (var value of matches) for (var match of parts(value)) {
      all.add(match);
      var root = match.startContainer.getRootNode(), target = root.nodeType === 9 ? root.head : root;
      if (target && !styled.has(target)) {
        styled.add(target);
        if (!target.querySelector("style[data-artifact-find-style]")) {
          var style = doc.createElement("style"); style.dataset.artifactFindStyle = ""; style.textContent = highlightCSS; target.append(style);
        }
      }
    }
    for (var match of parts(matches[active])) selected.add(match);
    registry.set("artifact-find-matches", all); registry.set("artifact-find-active", selected);
    owned = { registry: registry, all: all, active: selected };
  }
  function reveal(match) {
    match = first(match);
    if (!match) return;
    var element = match.startContainer.parentElement, doc = match.startContainer.ownerDocument, win = doc.defaultView;
    if (!element) return;
    function delta(start, end, low, high) {
      if (end - start > high - low) return start - low;
      return start < low ? start - low : end > high ? end - high : 0;
    }
    for (var parent = element; parent; parent = parent.assignedSlot || parent.parentElement || parent.getRootNode().host) {
      if (parent === doc.scrollingElement || parent === doc.documentElement) continue;
      var bounds = parent.getBoundingClientRect(), css = win.getComputedStyle(parent);
      // Geometry is in visual pixels, scrolling uses untransformed CSS pixels.
      var scaleX = parent.offsetWidth ? bounds.width / parent.offsetWidth : 1, scaleY = parent.offsetHeight ? bounds.height / parent.offsetHeight : 1;
      var rect = match.getBoundingClientRect(), top = bounds.top + parent.clientTop * scaleY, left = bounds.left + parent.clientLeft * scaleX;
      if (/(auto|scroll|hidden)/.test(css.overflowY)) parent.scrollTop += delta(rect.top, rect.bottom, top, top + parent.clientHeight * scaleY) / (scaleY || 1);
      rect = match.getBoundingClientRect();
      if (/(auto|scroll|hidden)/.test(css.overflowX)) parent.scrollLeft += delta(rect.left, rect.right, left, left + parent.clientWidth * scaleX) / (scaleX || 1);
    }
    var rect = match.getBoundingClientRect();
    var viewport = doc.compatMode === "CSS1Compat" ? doc.documentElement : undefined;
    var dx = delta(rect.left, rect.right, 0, viewport?.clientWidth || win.innerWidth), dy = delta(rect.top, rect.bottom, 0, viewport?.clientHeight || win.innerHeight);
    if (dx || dy) win.scrollBy(dx, dy);
  }
  // A light-DOM observer cannot see edits or attachShadow inside a component.
  // Discover open roots in bounded, single-flight tasks while Find is open.
  function observe(root, changed) {
    var win = root.ownerDocument.defaultView, stop = new AbortController(), watched = new Map(), timer, discoveryTimer, stopped = false;
    function ignored(change) {
      var element = change.target.nodeType === 1 ? change.target : change.target.parentElement;
      if (element?.closest("[role='search'], style[data-artifact-find-style]")) return true;
      return change.type === "childList" && [...change.addedNodes, ...change.removedNodes].every(function (node) { return node.nodeType === 1 && node.matches("style[data-artifact-find-style]"); });
    }
    function schedule() {
      if (stopped) return;
      clearTimeout(timer);
      timer = setTimeout(function () { if (!stopped) changed(); }, 100);
    }
    function* discover() {
      var stack = [root], seen = new Set(), live = new Set([root]), added = false, count = 0;
      while (stack.length) {
        var node = stack.pop();
        if (seen.has(node)) continue;
        seen.add(node);
        if (++count % 128 === 0) yield;
        if (node === root || node.nodeType === 11) {
          live.add(node);
          if (!watched.has(node)) {
            var observer = new win.MutationObserver(function (changes) { if (!changes.every(ignored)) schedule(); });
            observer.observe(node, { childList: true, characterData: true, subtree: true, attributes: true, attributeFilter: ["hidden", "aria-hidden", "class", "style", "open", "slot", "name", "data-editing", "data-artifact-find-unavailable"] });
            node.addEventListener("slotchange", schedule);
            watched.set(node, observer); added = watched.size > 1 || added;
          }
        }
        if (node.nodeType === 1 || node.nodeType === 11) for (var child of children(node)) stack.push(child);
      }
      for (var [node, observer] of watched) if (!live.has(node)) { observer.disconnect(); node.removeEventListener("slotchange", schedule); watched.delete(node); }
      if (added && !stopped) schedule();
    }
    function scan() {
      void cooperate(discover(), stop.signal).catch(function () {}).finally(function () {
        if (!stopped) discoveryTimer = setTimeout(scan, 500);
      });
    }
    scan();
    return function () {
      stopped = true; stop.abort(); clearTimeout(timer); clearTimeout(discoveryTimer);
      for (var [node, observer] of watched) { observer.disconnect(); node.removeEventListener("slotchange", schedule); }
      watched.clear();
    };
  }
  return {
    occurrences: function (source, query) { return sync(occurrenceSteps(source, query)); },
    occurrencesAsync: function (source, query, signal) { return cooperate(occurrenceSteps(source, query), signal); },
    blocks: function (root) { return sync(blockSteps(root)); },
    range: function (nodes, start, end) { return first(sync(mapHits(nodes, [{ start: start, end: end }]))[0]); },
    ranges: function (root, query) { return sync(searchSteps(root, query)).map(first); },
    search: function (root, query, signal) { return cooperate(searchSteps(root, query), signal); },
    anchor: anchor, retained: retained, paint: paint, clear: clear, reveal: reveal, observe: observe
  };
}
