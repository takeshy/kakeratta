// A deliberately small, dependency-free Mermaid renderer for the diagram
// forms people most often put in answers. It never interprets HTML: every
// label is assigned with textContent.
const NS = "http://www.w3.org/2000/svg";

const svg = (tag, attributes = {}, text = "") => {
  const node = document.createElementNS(NS, tag);
  for (const [name, value] of Object.entries(attributes)) node.setAttribute(name, value);
  if (text) node.textContent = text;
  return node;
};

const clean = (value) => value.trim().replace(/^['"]|['"]$/g, "").replace(/<[^>]*>/g, "");

function flowchart(source) {
  const lines = source.split("\n").map(line => line.trim()).filter(line => line && !line.startsWith("%%"));
  const direction = /^(?:flowchart|graph)\s+(LR|RL)/i.test(lines.shift() || "") ? "horizontal" : "vertical";
  const nodes = new Map(), edges = [];
  const remember = (raw) => {
    const match = raw.trim().match(/^([\w.-]+)(?:\(\((.*?)\)\)|\[(.*?)\]|\((.*?)\)|\{(.*?)\})?$/);
    if (!match) return null;
    const id = match[1], label = clean(match.slice(2).find(value => value !== undefined) || id);
    if (!nodes.has(id)) nodes.set(id, { id, label });
    return id;
  };
  for (const line of lines) {
    const match = line.match(/^(.+?)\s*(?:--\s*([^>-]+?)\s*)?(-->|---|-.->|==>)\s*(.+?)\s*;?$/);
    if (match) {
      const from = remember(match[1]), to = remember(match[4]);
      if (from && to) edges.push({ from, to, label: clean(match[2] || "") });
    } else remember(line.replace(/;$/, ""));
  }
  if (!nodes.size) return null;
  const width = direction === "horizontal" ? Math.max(320, nodes.size * 180) : 560;
  const height = direction === "horizontal" ? 180 : Math.max(180, nodes.size * 110);
  const root = svg("svg", { viewBox: `0 0 ${width} ${height}`, role: "img", "aria-label": "Mermaid flowchart" });
  const markerId = `arrow-${Math.random().toString(36).slice(2)}`;
  const defs = svg("defs"), marker = svg("marker", { id: markerId, viewBox: "0 0 10 10", refX: "9", refY: "5", markerWidth: "7", markerHeight: "7", orient: "auto-start-reverse" });
  marker.append(svg("path", { d: "M 0 0 L 10 5 L 0 10 z", class: "mermaid-arrow" })); defs.append(marker); root.append(defs);
  const positions = new Map();
  [...nodes.values()].forEach((node, index) => positions.set(node.id, direction === "horizontal"
    ? { x: 90 + index * 180, y: 90 } : { x: width / 2, y: 60 + index * 110 }));
  for (const edge of edges) {
    const a = positions.get(edge.from), b = positions.get(edge.to);
    root.append(svg("line", { x1: a.x, y1: a.y, x2: b.x, y2: b.y, class: "mermaid-edge", "marker-end": `url(#${markerId})` }));
    if (edge.label) root.append(svg("text", { x: (a.x + b.x) / 2 + 6, y: (a.y + b.y) / 2 - 7, class: "mermaid-edge-label" }, edge.label));
  }
  for (const node of nodes.values()) {
    const { x, y } = positions.get(node.id), boxWidth = Math.min(160, Math.max(90, node.label.length * 9 + 28));
    root.append(svg("rect", { x: x - boxWidth / 2, y: y - 24, width: boxWidth, height: 48, rx: "9", class: "mermaid-node" }));
    root.append(svg("text", { x, y: y + 5, class: "mermaid-node-label", "text-anchor": "middle" }, node.label));
  }
  return root;
}

function sequence(source) {
  const lines = source.split("\n").map(line => line.trim()).filter(line => line && !/^(?:sequenceDiagram|%%)/i.test(line));
  const actors = [], messages = [];
  const actor = id => { if (!actors.includes(id)) actors.push(id); };
  for (const line of lines) {
    const participant = line.match(/^(?:participant|actor)\s+(\S+)(?:\s+as\s+(.+))?$/i);
    if (participant) { actor(participant[1]); continue; }
    const message = line.match(/^(\S+?)\s*(-{1,2}>+|-->>|->>)\s*(\S+?)\s*:\s*(.+)$/);
    if (message) { actor(message[1]); actor(message[3]); messages.push({ from: message[1], to: message[3], label: clean(message[4]) }); }
  }
  if (!actors.length || !messages.length) return null;
  const width = Math.max(360, actors.length * 180), height = 100 + messages.length * 70;
  const root = svg("svg", { viewBox: `0 0 ${width} ${height}`, role: "img", "aria-label": "Mermaid sequence diagram" });
  const positions = new Map(actors.map((id, i) => [id, 90 + i * 180]));
  for (const id of actors) {
    const x = positions.get(id);
    root.append(svg("rect", { x: x - 65, y: 15, width: 130, height: 38, rx: "7", class: "mermaid-node" }));
    root.append(svg("text", { x, y: 40, class: "mermaid-node-label", "text-anchor": "middle" }, id));
    root.append(svg("line", { x1: x, y1: 53, x2: x, y2: height - 15, class: "mermaid-lifeline" }));
  }
  messages.forEach((message, index) => {
    const y = 85 + index * 70, from = positions.get(message.from), to = positions.get(message.to);
    root.append(svg("line", { x1: from, y1: y, x2: to, y2: y, class: "mermaid-edge" }));
    root.append(svg("text", { x: (from + to) / 2, y: y - 9, class: "mermaid-edge-label", "text-anchor": "middle" }, message.label));
    const sign = to > from ? -1 : 1;
    root.append(svg("path", { d: `M ${to} ${y} l ${sign * 10} -6 v 12 z`, class: "mermaid-arrow" }));
  });
  return root;
}

export function renderMermaid(source) {
  if (!document.createElementNS) return null;
  const trimmed = String(source || "").trim();
  if (/^(?:flowchart|graph)\b/i.test(trimmed)) return flowchart(trimmed);
  if (/^sequenceDiagram\b/i.test(trimmed)) return sequence(trimmed);
  return null;
}
