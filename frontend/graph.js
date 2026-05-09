/**
 * graph.js — Code Graph Explorer frontend
 *
 * Renders a force-directed graph of code symbols using D3 v7.
 * Communicates with the UI server REST API:
 *   GET /api/graph          – paginated nodes + edges
 *   GET /api/symbols        – symbol search
 *   GET /api/symbol/:id     – symbol detail + neighbours
 *   GET /api/projects       – project stats
 *   GET /api/search?q=...   – full-text search
 */

/* ── Colour map by symbol kind ─────────────────────────────────────────────── */
const KIND_COLOURS = {
  function: "var(--col-function)",
  method: "var(--col-method)",
  type: "var(--col-type)",
  interface: "var(--col-interface)",
  class: "var(--col-class)",
  variable: "var(--col-variable)",
  module: "var(--col-module)",
};
function kindColour(kind) {
  return KIND_COLOURS[kind] || "var(--col-default)";
}

/* ── DOM refs ──────────────────────────────────────────────────────────────── */
const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => document.querySelectorAll(sel);

const searchInput = $("#search-input");
const searchResults = $("#search-results");
const kindFilter = $("#kind-filter");
const projectFilter = $("#project-filter");
const detailPanel = $("#detail-panel");
const detailClose = $("#detail-close");
const detailName = $("#detail-name");
const detailMeta = $("#detail-meta");
const callersList = $("#callers-list");
const calleesList = $("#callees-list");
const projectsList = $("#projects-list");
const statsBadge = $("#stats-badge");
const graphLoading = $("#graph-loading");
const graphEmpty = $("#graph-empty");
const btnZoomIn = $("#btn-zoom-in");
const btnZoomOut = $("#btn-zoom-out");
const btnZoomReset = $("#btn-zoom-reset");
const btnLayoutReset = $("#btn-layout-reset");

/* ── State ─────────────────────────────────────────────────────────────────── */
let allNodes = [];
let allLinks = [];
let simulation = null;
let zoomBehaviour = null;
let selectedNodeId = null;

/* ── SVG / D3 setup ────────────────────────────────────────────────────────── */
const svg = d3.select("#graph-svg");
const root = d3.select("#graph-root");

zoomBehaviour = d3
  .zoom()
  .scaleExtent([0.05, 4])
  .on("zoom", (event) => root.attr("transform", event.transform));

svg.call(zoomBehaviour);

/* ── API helpers ───────────────────────────────────────────────────────────── */
async function apiFetch(path, timeoutMs = 30000) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  try {
    const res = await fetch(path, { signal: controller.signal });
    if (!res.ok)
      throw new Error(`API ${path} → ${res.status} ${res.statusText}`);
    return res.json();
  } catch (err) {
    if (err.name === "AbortError")
      throw new Error(`API ${path} timed out after ${timeoutMs}ms`);
    throw err;
  } finally {
    clearTimeout(timer);
  }
}

/* ── Bootstrap ─────────────────────────────────────────────────────────────── */
async function init() {
  showLoading(true);
  showError(null);
  try {
    await Promise.all([loadProjects(), loadGraph()]);
  } catch (err) {
    console.error("init error:", err);
    showError(err.message || String(err));
    showEmpty(false);
  } finally {
    showLoading(false);
  }
}

/* ── Projects ──────────────────────────────────────────────────────────────── */
async function loadProjects() {
  const projects = await apiFetch("/api/projects");
  renderProjects(projects);
  populateProjectFilter(projects);
}

function renderProjects(projects) {
  projectsList.innerHTML = "";
  if (!projects || projects.length === 0) {
    projectsList.innerHTML =
      '<li class="project-item"><span class="proj-name" style="color:var(--text-muted)">No projects</span></li>';
    return;
  }
  for (const p of projects) {
    const li = document.createElement("li");
    li.className = "project-item";
    li.innerHTML = `
      <div class="proj-name">${escHtml(p.ProjectID || p.project_id || "—")}</div>
      <div class="proj-stats">
        ${p.SymbolCount ?? p.symbol_count ?? 0} symbols ·
        ${p.EdgeCount ?? p.edge_count ?? 0} edges ·
        ${p.FileCount ?? p.file_count ?? 0} files
      </div>`;
    projectsList.appendChild(li);
  }
}

function populateProjectFilter(projects) {
  // Remove old dynamic options
  while (projectFilter.options.length > 1) projectFilter.remove(1);
  for (const p of projects || []) {
    const id = p.ProjectID || p.project_id || "";
    if (!id) continue;
    const opt = document.createElement("option");
    opt.value = id;
    opt.textContent = id;
    projectFilter.appendChild(opt);
  }
}

/* ── Graph data ────────────────────────────────────────────────────────────── */
async function loadGraph(page = 1, pageSize = 100, projectID = "", kind = "") {
  let url = `/api/graph?page=${page}&page_size=${pageSize}`;
  if (projectID) url += `&project_id=${encodeURIComponent(projectID)}`;
  if (kind) url += `&kind=${encodeURIComponent(kind)}`;

  const data = await apiFetch(url);
  allNodes = (data.nodes || []).map((n) => ({ ...n, id: n.ID || n.id }));

  // Build a node ID set for fast lookup — edges may reference nodes not on
  // this page (outgoing edges to other pages), so we add ghost nodes for them.
  const nodeMap = new Map(allNodes.map((n) => [n.id, n]));

  allLinks = (data.edges || []).map((e) => ({
    source: e.FromID || e.from_id,
    target: e.ToID || e.to_id,
    kind: e.Kind || e.kind,
  }));

  // Add ghost nodes for edge targets not in the current page so D3 can render
  // the links without crashing.
  for (const link of allLinks) {
    if (!nodeMap.has(link.target)) {
      const ghost = {
        id: link.target,
        ID: link.target,
        Name: link.target.slice(0, 8),
        Kind: "unknown",
        ghost: true,
      };
      allNodes.push(ghost);
      nodeMap.set(link.target, ghost);
    }
  }

  const totalSymbols = data.total_nodes ?? allNodes.length;
  const totalEdges = data.total_edges ?? allLinks.length;
  statsBadge.textContent = `${totalSymbols} symbols · ${totalEdges} edges`;

  if (allNodes.length === 0) {
    showEmpty(true);
    return;
  }
  showEmpty(false);
  renderGraph(allNodes, allLinks);
}

/* ── Force-directed graph ──────────────────────────────────────────────────── */
function renderGraph(nodes, links) {
  root.selectAll("*").remove();

  // Filter links to only those whose source and target exist in nodes
  const nodeIds = new Set(nodes.map((n) => n.id));
  const validLinks = links.filter(
    (l) => nodeIds.has(l.source) && nodeIds.has(l.target),
  );

  // Draw links
  const linkSel = root
    .append("g")
    .attr("class", "links")
    .selectAll("line")
    .data(validLinks)
    .join("line")
    .attr("class", "graph-link");

  // Draw nodes
  const nodeSel = root
    .append("g")
    .attr("class", "nodes")
    .selectAll("g")
    .data(nodes)
    .join("g")
    .attr("class", (d) => (d.ghost ? "graph-node ghost" : "graph-node"))
    .attr("role", "button")
    .attr("tabindex", "0")
    .attr("aria-label", (d) => `${d.Name || d.name} (${d.Kind || d.kind})`)
    .on("click", (event, d) => selectNode(d))
    .on("keydown", (event, d) => {
      if (event.key === "Enter" || event.key === " ") selectNode(d);
    })
    .call(
      d3
        .drag()
        .on("start", dragStarted)
        .on("drag", dragged)
        .on("end", dragEnded),
    );

  nodeSel
    .append("circle")
    .attr("r", (d) => nodeRadius(d))
    .attr("fill", (d) => kindColour(d.Kind || d.kind))
    .attr("stroke", (d) => kindColour(d.Kind || d.kind));

  nodeSel
    .append("text")
    .attr("dy", (d) => nodeRadius(d) + 3)
    .text((d) => truncate(d.Name || d.name || "", 18));

  // Simulation
  if (simulation) simulation.stop();

  simulation = d3
    .forceSimulation(nodes)
    .force(
      "link",
      d3
        .forceLink(validLinks)
        .id((d) => d.id)
        .distance(80)
        .strength(0.4),
    )
    .force("charge", d3.forceManyBody().strength(-120))
    .force(
      "center",
      d3.forceCenter(
        svg.node().clientWidth / 2 || 600,
        svg.node().clientHeight / 2 || 400,
      ),
    )
    .force(
      "collision",
      d3.forceCollide().radius((d) => nodeRadius(d) + 6),
    )
    .on("tick", () => {
      linkSel
        .attr("x1", (d) => d.source.x)
        .attr("y1", (d) => d.source.y)
        .attr("x2", (d) => d.target.x)
        .attr("y2", (d) => d.target.y);

      nodeSel.attr("transform", (d) => `translate(${d.x},${d.y})`);
    });
}

function nodeRadius(d) {
  const kind = d.Kind || d.kind || "";
  if (kind === "interface" || kind === "class" || kind === "type") return 9;
  if (kind === "module") return 11;
  return 7;
}

function dragStarted(event, d) {
  if (!event.active) simulation.alphaTarget(0.3).restart();
  d.fx = d.x;
  d.fy = d.y;
}
function dragged(event, d) {
  d.fx = event.x;
  d.fy = event.y;
}
function dragEnded(event, d) {
  if (!event.active) simulation.alphaTarget(0);
  d.fx = null;
  d.fy = null;
}

/* ── Node selection / detail ───────────────────────────────────────────────── */
async function selectNode(d) {
  const id = d.id || d.ID;
  selectedNodeId = id;

  // Highlight selected node
  d3.selectAll(".graph-node").classed("selected", (n) => (n.id || n.ID) === id);

  // Highlight connected links
  d3.selectAll(".graph-link").classed(
    "highlighted",
    (l) => (l.source.id || l.source) === id || (l.target.id || l.target) === id,
  );

  try {
    const detail = await apiFetch(`/api/symbol/${encodeURIComponent(id)}`);
    renderDetail(detail);
  } catch (err) {
    console.error("symbol detail error:", err);
  }
}

function renderDetail(detail) {
  const sym = detail.symbol || detail;
  if (!sym) return;

  detailName.textContent = sym.Name || sym.name || "—";

  detailMeta.innerHTML = `
    <dt>Kind</dt>    <dd>${escHtml(sym.Kind || sym.kind || "—")}</dd>
    <dt>File</dt>    <dd>${escHtml(sym.File || sym.file || "—")}</dd>
    <dt>Lines</dt>   <dd>${sym.StartLine ?? sym.start_line ?? "?"}–${sym.EndLine ?? sym.end_line ?? "?"}</dd>
    <dt>Project</dt> <dd>${escHtml(sym.ProjectID || sym.project_id || "—")}</dd>
    ${sym.Signature || sym.signature ? `<dt>Sig</dt><dd><code>${escHtml(sym.Signature || sym.signature)}</code></dd>` : ""}
  `;

  renderNeighbourList(callersList, detail.callers || []);
  renderNeighbourList(calleesList, detail.callees || []);

  detailPanel.hidden = false;
}

function renderNeighbourList(ul, symbols) {
  ul.innerHTML = "";
  if (!symbols.length) {
    ul.innerHTML =
      '<li style="color:var(--text-muted);font-size:0.75rem">None</li>';
    return;
  }
  for (const s of symbols) {
    const li = document.createElement("li");
    const a = document.createElement("a");
    a.textContent = s.Name || s.name || s.ID || s.id;
    a.title = s.File || s.file || "";
    a.addEventListener("click", () => {
      const node = allNodes.find((n) => (n.id || n.ID) === (s.ID || s.id));
      if (node) selectNode(node);
    });
    li.appendChild(a);
    ul.appendChild(li);
  }
}

detailClose.addEventListener("click", () => {
  detailPanel.hidden = true;
  selectedNodeId = null;
  d3.selectAll(".graph-node").classed("selected", false);
  d3.selectAll(".graph-link").classed("highlighted", false);
});

/* ── Search ────────────────────────────────────────────────────────────────── */
let searchTimer = null;

searchInput.addEventListener("input", () => {
  clearTimeout(searchTimer);
  const q = searchInput.value.trim();
  if (!q) {
    closeSearch();
    return;
  }
  searchTimer = setTimeout(() => runSearch(q), 250);
});

searchInput.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    closeSearch();
    searchInput.blur();
  }
  if (e.key === "ArrowDown") {
    const first = searchResults.querySelector(".search-item");
    if (first) first.focus();
    e.preventDefault();
  }
});

searchResults.addEventListener("keydown", (e) => {
  const items = [...searchResults.querySelectorAll(".search-item")];
  const idx = items.indexOf(document.activeElement);
  if (e.key === "ArrowDown" && idx < items.length - 1) {
    items[idx + 1].focus();
    e.preventDefault();
  }
  if (e.key === "ArrowUp") {
    idx > 0 ? items[idx - 1].focus() : searchInput.focus();
    e.preventDefault();
  }
  if (e.key === "Escape") {
    closeSearch();
    searchInput.focus();
  }
});

document.addEventListener("click", (e) => {
  if (!searchInput.contains(e.target) && !searchResults.contains(e.target))
    closeSearch();
});

async function runSearch(q) {
  try {
    const results = await apiFetch(`/api/search?q=${encodeURIComponent(q)}`);
    renderSearchResults(results || []);
  } catch (err) {
    console.error("search error:", err);
  }
}

function renderSearchResults(symbols) {
  searchResults.innerHTML = "";
  if (!symbols.length) {
    searchResults.innerHTML =
      '<div class="search-item" style="color:var(--text-muted)">No results</div>';
    searchResults.classList.add("open");
    return;
  }
  for (const s of symbols) {
    const div = document.createElement("div");
    div.className = "search-item";
    div.setAttribute("role", "option");
    div.setAttribute("tabindex", "0");
    div.innerHTML = `
      <span class="kind-dot" style="background:${kindColour(s.Kind || s.kind)}"></span>
      <span>
        <div class="sym-name">${escHtml(s.Name || s.name || "")}</div>
        <div class="sym-file">${escHtml(s.File || s.file || "")}</div>
      </span>`;
    div.addEventListener("click", () => focusSymbol(s));
    div.addEventListener("keydown", (e) => {
      if (e.key === "Enter") focusSymbol(s);
    });
    searchResults.appendChild(div);
  }
  searchResults.classList.add("open");
}

function closeSearch() {
  searchResults.classList.remove("open");
  searchResults.innerHTML = "";
}

function focusSymbol(s) {
  closeSearch();
  const id = s.ID || s.id;
  const node = allNodes.find((n) => (n.id || n.ID) === id);
  if (node) {
    // Pan to node
    const w = svg.node().clientWidth || 800;
    const h = svg.node().clientHeight || 600;
    svg
      .transition()
      .duration(500)
      .call(
        zoomBehaviour.transform,
        d3.zoomIdentity.translate(w / 2 - node.x, h / 2 - node.y).scale(1),
      );
    selectNode(node);
  } else {
    // Node not in current graph view — show detail directly
    selectNode({ id, ID: id, Name: s.Name || s.name, Kind: s.Kind || s.kind });
  }
}

/* ── Filters ───────────────────────────────────────────────────────────────── */
kindFilter.addEventListener("change", applyFilters);
projectFilter.addEventListener("change", applyFilters);

async function applyFilters() {
  const kind = kindFilter.value;
  const project = projectFilter.value;

  showLoading(true);
  showError(null);
  try {
    await loadGraph(1, 100, project, kind);
  } catch (err) {
    console.error("filter error:", err);
    showError(err.message || String(err));
  } finally {
    showLoading(false);
  }
}

/* ── Zoom controls ─────────────────────────────────────────────────────────── */
btnZoomIn.addEventListener("click", () =>
  svg.transition().call(zoomBehaviour.scaleBy, 1.4),
);
btnZoomOut.addEventListener("click", () =>
  svg.transition().call(zoomBehaviour.scaleBy, 1 / 1.4),
);
btnZoomReset.addEventListener("click", () =>
  svg.transition().call(zoomBehaviour.transform, d3.zoomIdentity),
);
btnLayoutReset.addEventListener("click", () => {
  if (simulation) {
    simulation.alpha(1).restart();
  }
});

/* ── Helpers ───────────────────────────────────────────────────────────────── */
function showLoading(on) {
  graphLoading.hidden = !on;
}
function showEmpty(on) {
  graphEmpty.hidden = !on;
}
function showError(msg) {
  const el = $("#graph-error");
  if (!el) return;
  if (!msg) {
    el.hidden = true;
    return;
  }
  el.hidden = false;
  el.textContent = "Error: " + msg;
}

function escHtml(str) {
  return String(str)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

function truncate(str, max) {
  return str.length > max ? str.slice(0, max - 1) + "…" : str;
}

/* ── Start ─────────────────────────────────────────────────────────────────── */
init();
