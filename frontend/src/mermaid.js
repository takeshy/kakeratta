// A deliberately small, dependency-free Mermaid renderer for the diagram
// forms people most often put in answers: flowcharts and sequence diagrams.
// It reads the syntax models actually write (multi-line and <br> labels,
// pipe-labelled, chained and fanned-out edges, subgraphs, notes), lays
// flowcharts out in layers, and never interprets HTML: every label is
// assigned with textContent. A statement it cannot read keeps the whole
// diagram as source rather than drawing part of it.
const NS = "http://www.w3.org/2000/svg";

const svg = (tag, attributes = {}, text = "") => {
  const node = document.createElementNS(NS, tag);
  for (const [name, value] of Object.entries(attributes)) node.setAttribute(name, value);
  if (text) node.textContent = text;
  return node;
};

// Labels: quotes and markup go, <br> is a line break, and the text wraps.
const clean = (value) => String(value || "").trim().replace(/^['"]|['"]$/g, "")
  .replace(/<br\s*\/?>/gi, "\n").replace(/<[^>]*>/g, "").replace(/[ \t]*\n[ \t]*/g, "\n").trim();
const WIDE = /[ᄀ-ᅟ⺀-꓏가-힣豈-﫿︰-﹏＀-｠￠-￦]|\p{Extended_Pictographic}/u;
const units = (char) => WIDE.test(char) ? 2 : 1;
function wrap(label, limit = 22) {
  const lines = [];
  for (const paragraph of String(label).split("\n")) {
    let line = "", used = 0;
    for (const char of paragraph) {
      if (used + units(char) > limit && line) { lines.push(line); line = ""; used = 0; }
      line += char; used += units(char);
    }
    lines.push(line);
  }
  return lines.filter((line, index) => line || index === 0);
}
const widthOf = (lines) => Math.max(...lines.map(line => [...line].reduce((sum, char) => sum + units(char), 0))) * 7.4;
const LINE = 18;

// One text element per label; a wrapped label stacks tspans around y.
function label(lines, x, y, className, anchor = "middle") {
  const node = svg("text", { x, y: y - (lines.length - 1) * LINE / 2 + 5, class: className, "text-anchor": anchor });
  if (lines.length === 1) node.textContent = lines[0];
  else lines.forEach((line, index) => node.append(svg("tspan", { x, dy: index ? LINE : 0 }, line)));
  return node;
}

// Statements, with labels that run over several lines joined into one.
function statements(source) {
  const out = [];
  let pending = "", depth = 0;
  for (const raw of source.split("\n")) {
    const line = raw.trim();
    if (!pending && (!line || line.startsWith("%%"))) continue;
    pending = pending ? `${pending}\n${line}` : line;
    for (const char of line) {
      if ("[({".includes(char)) depth++;
      else if ("])}".includes(char)) depth = Math.max(0, depth - 1);
    }
    // An unclosed bracket carries on to the next line, but not forever.
    if (depth > 0 && pending.split("\n").length < 6) continue;
    out.push(pending.replace(/;\s*$/, ""));
    pending = ""; depth = 0;
  }
  if (pending) out.push(pending);
  return out;
}

const NODE = /^([\p{L}\p{N}_.]+(?:-(?!-|\.)[\p{L}\p{N}_.]+)*)\s*(?:\(\(([\s\S]*?)\)\)|\[\[([\s\S]*?)\]\]|\[\(([\s\S]*?)\)\]|\(\[([\s\S]*?)\]\)|\[([\s\S]*?)\]|\(([\s\S]*?)\)|\{\{([\s\S]*?)\}\}|\{([\s\S]*?)\}|>([\s\S]*?)\])?\s*/u;
const ARROW = /^(?:(--|==|-\.)\s+([^|\n]+?)\s+)?(-{2,}>|\.-+>|-\.+->|-{3,}|\.-+|-\.+-|={2,}>|={3,})\s*(?:\|([^|]*)\|)?\s*/;
const IGNORED = /^(?:subgraph\b|end$|direction\s|style\s|classDef\s|class\s|linkStyle\s|click\s)/i;

function flowchart(source) {
  const [head, ...rest] = statements(source);
  const horizontal = /^(?:flowchart|graph)\s+(LR|RL)\b/i.test(head || "");
  const nodes = new Map(), edges = [];
  const node = (text) => {
    const match = text.match(NODE);
    if (!match) return null;
    const id = match[1], given = match.slice(2).find(value => value !== undefined);
    if (!nodes.has(id)) nodes.set(id, { id, label: id, decision: false, order: nodes.size });
    if (given !== undefined) Object.assign(nodes.get(id), { label: clean(given) || id, decision: match[0].trimStart().slice(id.length).trimStart().startsWith("{") });
    return { id, length: match[0].length };
  };
  // A group is one node or several joined by "&".
  const group = (text) => {
    const ids = [];
    let rest = text;
    for (;;) {
      const found = node(rest);
      if (!found) return null;
      ids.push(found.id);
      rest = rest.slice(found.length);
      const and = rest.match(/^&\s*/);
      if (!and) return { ids, rest };
      rest = rest.slice(and[0].length);
    }
  };
  for (const statement of rest) {
    if (IGNORED.test(statement)) continue;
    let from = group(statement);
    if (!from) return null;
    let text = from.rest;
    while (text) {
      const arrow = text.match(ARROW);
      if (!arrow) return null;
      const to = group(text.slice(arrow[0].length));
      if (!to) return null;
      const kind = arrow[3].startsWith("=") ? "thick" : arrow[3].includes(".") ? "dotted" : "plain";
      for (const a of from.ids) {
        for (const b of to.ids) {
          if (a === b) return null;
          edges.push({ from: a, to: b, label: clean(arrow[2] || arrow[4] || ""), directed: arrow[3].endsWith(">"), kind });
        }
      }
      from = to;
      text = to.rest;
    }
  }
  if (!nodes.size) return null;
  return layOut([...nodes.values()], edges, horizontal);
}

// Layers by longest path from the sources, ignoring edges that close a cycle,
// then one pass that orders each layer by where its predecessors sit.
function layOut(nodes, edges, horizontal) {
  const outgoing = new Map(nodes.map(node => [node.id, []]));
  const state = new Map(), forward = [];
  const visit = (id) => {
    state.set(id, 1);
    for (const edge of edges.filter(edge => edge.from === id)) {
      if (state.get(edge.to) === 1) continue;
      forward.push(edge);
      outgoing.get(id).push(edge.to);
      if (!state.has(edge.to)) visit(edge.to);
    }
    state.set(id, 2);
  };
  for (const node of nodes) if (!state.has(node.id)) visit(node.id);
  const rank = new Map(nodes.map(node => [node.id, 0]));
  for (let pass = 0; pass < nodes.length; pass++) {
    let moved = false;
    for (const edge of forward) {
      if (rank.get(edge.to) < rank.get(edge.from) + 1) { rank.set(edge.to, rank.get(edge.from) + 1); moved = true; }
    }
    if (!moved) break;
  }
  const layers = [];
  for (const node of nodes) (layers[rank.get(node.id)] ||= []).push(node);
  for (const node of nodes) {
    node.lines = wrap(node.label);
    node.w = Math.min(200, Math.max(90, widthOf(node.lines) + 28));
    node.h = Math.max(48, node.lines.length * LINE + 26);
  }
  const place = new Map();
  layers.forEach((layer, index) => {
    if (index) {
      const center = (node) => {
        const from = forward.filter(edge => edge.to === node.id).map(edge => place.get(edge.from)).filter(value => value !== undefined);
        return from.length ? from.reduce((a, b) => a + b, 0) / from.length : node.order;
      };
      layer.sort((a, b) => center(a) - center(b) || a.order - b.order);
    }
    layer.forEach((node, position) => place.set(node.id, position));
  });
  // Across a layer nodes sit side by side; layers follow one another. A
  // top-down layer too wide for a timeline wraps onto further rows, and a
  // left-to-right chart too wide for one turns top-down.
  const arrange = (horizontal) => {
    const across = (node) => horizontal ? node.h : node.w, along = (node) => horizontal ? node.w : node.h;
    const gapAcross = horizontal ? 24 : 28, gapAlong = 56, margin = 16;
    const rows = [];
    for (const layer of layers) {
      let row = [], span = 0;
      for (const node of layer) {
        if (!horizontal && row.length && span + gapAcross + across(node) > 600) { rows.push(row); row = []; span = 0; }
        span += (row.length ? gapAcross : 0) + across(node);
        row.push(node);
      }
      rows.push(row);
    }
    const spans = rows.map(row => row.reduce((sum, node) => sum + across(node), 0) + gapAcross * (row.length - 1));
    const widest = Math.max(...spans);
    const positions = new Map();
    let offset = margin;
    rows.forEach((row, index) => {
      const depth = Math.max(...row.map(along));
      let cursor = margin + (widest - spans[index]) / 2;
      for (const node of row) {
        const a = cursor + across(node) / 2, b = offset + depth / 2;
        positions.set(node.id, horizontal ? { x: b, y: a } : { x: a, y: b });
        cursor += across(node) + gapAcross;
      }
      offset += depth + gapAlong;
    });
    return {
      positions,
      width: Math.ceil(horizontal ? offset - gapAlong + margin : widest + margin * 2),
      height: Math.ceil(horizontal ? widest + margin * 2 : offset - gapAlong + margin),
    };
  };
  let arranged = arrange(horizontal);
  if (horizontal && arranged.width > 640) arranged = arrange(false);
  const { positions, width, height } = arranged;
  const root = svg("svg", { viewBox: `0 0 ${width} ${height}`, width, height, role: "img", "aria-label": "Mermaid flowchart" });
  const markerId = `arrow-${Math.random().toString(36).slice(2)}`;
  const defs = svg("defs"), marker = svg("marker", { id: markerId, viewBox: "0 0 10 10", refX: "10", refY: "5", markerWidth: "7", markerHeight: "7", orient: "auto-start-reverse" });
  marker.append(svg("path", { d: "M 0 0 L 10 5 L 0 10 z", class: "mermaid-arrow" })); defs.append(marker); root.append(defs);
  const byId = new Map(nodes.map(node => [node.id, node]));
  // An edge starts and ends one pixel outside its boxes, so tips show.
  const boundary = (center, other, node) => {
    const dx = other.x - center.x, dy = other.y - center.y;
    const scale = 1 / Math.max(Math.abs(dx) / (node.w / 2 + 1), Math.abs(dy) / (node.h / 2 + 1));
    return { x: center.x + dx * scale, y: center.y + dy * scale };
  };
  const labels = [];
  for (const edge of edges) {
    const a = positions.get(edge.from), b = positions.get(edge.to);
    const start = boundary(a, b, byId.get(edge.from)), end = boundary(b, a, byId.get(edge.to));
    root.append(svg("line", { x1: start.x, y1: start.y, x2: end.x, y2: end.y, class: `mermaid-edge${edge.kind === "plain" ? "" : ` mermaid-edge-${edge.kind}`}`, ...(edge.directed ? { "marker-end": `url(#${markerId})` } : {}) }));
    if (edge.label) labels.push({ lines: wrap(edge.label, 16), x: (start.x + end.x) / 2, y: (start.y + end.y) / 2 });
  }
  for (const node of nodes) {
    const { x, y } = positions.get(node.id);
    root.append(svg("rect", { x: x - node.w / 2, y: y - node.h / 2, width: node.w, height: node.h, rx: node.decision ? "18" : "9", class: `mermaid-node${node.decision ? " mermaid-decision" : ""}` }));
    root.append(label(node.lines, x, y, "mermaid-node-label"));
  }
  // Edge labels go last, on a plate, so lines and boxes never hide them.
  for (const { lines, x, y } of labels) {
    const w = widthOf(lines) + 12, h = lines.length * LINE + 6;
    root.append(svg("rect", { x: x - w / 2, y: y - h / 2, width: w, height: h, rx: "5", class: "mermaid-edge-plate" }));
    root.append(label(lines, x, y, "mermaid-edge-label"));
  }
  return root;
}

const PARTICIPANT = /^(?:participant|actor)\s+(.+?)(?:\s+as\s+(.+))?$/i;
const MESSAGE = /^(.+?)\s*(-->>|->>|-->|->|--x|-x|--\)|-\))\s*[+-]?\s*(.+?)\s*:\s*(.*)$/;
const NOTE = /^note\s+(over|left of|right of)\s+([^:]+?)\s*:\s*(.*)$/i;
const CONTROL = /^(?:loop|alt|else|opt|par|and|critical|break|end|rect|activate|deactivate|autonumber|box)\b/i;

function sequence(source) {
  const lines = source.split("\n").map(line => line.trim()).filter(line => line && !/^(?:sequenceDiagram|%%)/i.test(line));
  const actors = [], names = new Map(), rows = [];
  const actor = id => { if (!actors.includes(id)) actors.push(id); };
  for (const line of lines) {
    const participant = line.match(PARTICIPANT);
    if (participant) {
      actor(participant[1]);
      names.set(participant[1], clean(participant[2] || participant[1]));
      continue;
    }
    const note = line.match(NOTE);
    if (note) {
      const over = note[2].split(",").map(id => id.trim()).filter(Boolean);
      over.forEach(actor);
      rows.push({ note: note[1].toLowerCase(), over, label: clean(note[3]) });
      continue;
    }
    const message = line.match(MESSAGE);
    if (message) {
      actor(message[1]); actor(message[3]);
      rows.push({ from: message[1], to: message[3], label: clean(message[4]), dashed: message[2].startsWith("--") });
      continue;
    }
    if (CONTROL.test(line)) continue;
    // A line that is none of these continues the message or note above it.
    if (rows.length) { rows.at(-1).label += `\n${clean(line)}`; continue; }
    return null;
  }
  if (!actors.length || !rows.some(row => row.from)) return null;
  const width = Math.max(360, actors.length * 180);
  const x = new Map(actors.map((id, i) => [id, 90 + i * 180]));
  for (const row of rows) {
    row.lines = wrap(row.label, row.from ? 26 : 30);
    row.height = Math.max(70, 52 + row.lines.length * LINE);
  }
  const height = 70 + rows.reduce((sum, row) => sum + row.height, 0);
  const root = svg("svg", { viewBox: `0 0 ${width} ${height}`, width, height, role: "img", "aria-label": "Mermaid sequence diagram" });
  for (const id of actors) {
    const lines = wrap(names.get(id) || id, 16), boxHeight = Math.max(38, lines.length * LINE + 18);
    root.append(svg("line", { x1: x.get(id), y1: 15 + boxHeight, x2: x.get(id), y2: height - 15, class: "mermaid-lifeline" }));
    root.append(svg("rect", { x: x.get(id) - 65, y: 15, width: 130, height: boxHeight, rx: "7", class: "mermaid-node" }));
    root.append(label(lines, x.get(id), 15 + boxHeight / 2, "mermaid-node-label"));
  }
  let top = 70;
  for (const row of rows) {
    const y = top + row.height - 18;
    if (row.note) {
      const xs = row.over.map(id => x.get(id));
      const left = row.note === "right of" ? Math.max(...xs) + 10 : row.note === "left of" ? Math.min(...xs) - 150 : Math.min(...xs) - 75;
      const right = row.note === "right of" ? left + 140 : row.note === "left of" ? Math.min(...xs) - 10 : Math.max(...xs) + 75;
      const h = row.lines.length * LINE + 12;
      root.append(svg("rect", { x: left, y: top + row.height / 2 - h / 2, width: right - left, height: h, rx: "4", class: "mermaid-note" }));
      root.append(label(row.lines, (left + right) / 2, top + row.height / 2, "mermaid-edge-label"));
    } else if (row.from === row.to) {
      const at = x.get(row.from);
      root.append(svg("path", { d: `M ${at} ${y - 14} h 40 v 14 h -40`, class: `mermaid-edge${row.dashed ? " mermaid-edge-dotted" : ""}`, fill: "none" }));
      root.append(svg("path", { d: `M ${at} ${y} l 10 -6 v 12 z`, class: "mermaid-arrow" }));
      root.append(label(row.lines, at + 46, y - 7 - (row.lines.length - 1) * LINE / 2 - 4, "mermaid-edge-label", "start"));
    } else {
      const from = x.get(row.from), to = x.get(row.to);
      root.append(svg("line", { x1: from, y1: y, x2: to, y2: y, class: `mermaid-edge${row.dashed ? " mermaid-edge-dotted" : ""}` }));
      root.append(label(row.lines, (from + to) / 2, y - 9 - (row.lines.length - 1) * LINE / 2 - 5, "mermaid-edge-label"));
      const sign = to > from ? -1 : 1;
      root.append(svg("path", { d: `M ${to} ${y} l ${sign * 10} -6 v 12 z`, class: "mermaid-arrow" }));
    }
    top += row.height;
  }
  return root;
}

export function renderMermaid(source) {
  if (!document.createElementNS) return null;
  const trimmed = String(source || "").trim();
  if (/^(?:flowchart|graph)\b/i.test(trimmed)) return flowchart(trimmed);
  if (/^sequenceDiagram\b/i.test(trimmed)) return sequence(trimmed);
  return null;
}
