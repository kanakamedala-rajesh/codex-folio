// Disposable #58 experiment, deliberately independent of production code.
const $ = (id) => document.getElementById(id);
const start = Date.UTC(2025, 7, 1);
const end = Date.UTC(2026, 8, 1);
const interval = 30 * 60 * 1000;
const pageSize = 48;
const fixture = Array.from({ length: (end - start) / interval }, (_, i) => ({
  capturedAt: start + i * interval,
  fiveHour: (i % 997 < 8 || (i >= 5760 && i < 5856)) ? null : i === 10 ? 0 : Math.max(0, 100 - (i % 10) * 9),
  weekly: (i % 997 < 8 || (i >= 5760 && i < 5856)) ? null : Math.max(0, 100 - Math.floor((i % 336) / 4)),
}));
let records = fixture;
let selected = 0;
let page = 0;
let dateFormat;
let numberFormat;
const valueText = (value) => value === null ? "Temporarily unavailable" : numberFormat.format(value / 100);
const dateText = (record) => dateFormat.format(record.capturedAt);

function showSelection() {
  const record = records[selected];
  const description = `${dateText(record)} UTC; 5-hour remaining ${valueText(record.fiveHour)}; weekly remaining ${valueText(record.weekly)}; Provider reported.`;
  $("sample").value = selected;
  $("sample").setAttribute("aria-valuetext", description);
  $("selection").textContent = description;
  $("data").querySelector("caption").textContent = `${$("profile").selectedOptions[0].textContent} · Provider reported · ${records.length.toLocaleString($("locale").value)} captured samples · UTC · Gaps are temporarily unavailable`;
  const rows = records.slice(page * pageSize, (page + 1) * pageSize);
  $("data").querySelector("tbody").replaceChildren(...rows.map((record, offset) => {
    const row = document.createElement("tr");
    const current = page * pageSize + offset === selected;
    if (current) row.dataset.current = "true";
    [dateText(record) + (current ? " · Selected" : ""), valueText(record.fiveHour), valueText(record.weekly)].forEach((text) => {
      const cell = document.createElement("td");
      cell.textContent = text;
      row.append(cell);
    });
    return row;
  }));
  $("previous").disabled = page === 0;
  $("next").disabled = (page + 1) * pageSize >= records.length;
  $("page-status").textContent = `Samples ${page * pageSize + 1}–${Math.min((page + 1) * pageSize, records.length)} of ${records.length}`;
}

function draw() {
  const began = performance.now();
  const style = getComputedStyle(document.body);
  const colors = [style.getPropertyValue("--cyan").trim(), style.getPropertyValue("--magenta").trim()];
  const width = Math.max(280, $("plot").clientWidth);
  const height = 260;
  const x = (i) => 40 + i / Math.max(1, records.length - 1) * (width - 56);
  const y = (value) => 16 + (100 - value) / 100 * (height - 44);
  if ($("renderer").value === "svg") {
  const svgNS = "http://www.w3.org/2000/svg";
  const element = document.createElementNS(svgNS, "svg");
  element.setAttribute("viewBox", `0 0 ${width} ${height}`);
  element.setAttribute("aria-hidden", "true");
  element.setAttribute("focusable", "false");
  for (const tick of [0, 25, 50, 75, 100]) {
    const line = document.createElementNS(svgNS, "line");
    Object.entries({ x1: 40, x2: width - 16, y1: y(tick), y2: y(tick), stroke: style.getPropertyValue("--rule").trim() }).forEach(([key, value]) => line.setAttribute(key, value));
    const label = document.createElementNS(svgNS, "text");
    Object.entries({ x: 1, y: y(tick) + 4, fill: style.color, "font-size": 14 }).forEach(([key, value]) => label.setAttribute(key, value));
    label.textContent = tick;
    element.append(line, label);
  }
  ["fiveHour", "weekly"].forEach((field, series) => {
    let continuous = false;
    const d = records.map((record, i) => {
      if (record[field] === null) { continuous = false; return ""; }
      const command = continuous ? "L" : "M";
      continuous = true;
      return `${command}${x(i).toFixed(2)},${y(record[field]).toFixed(2)}`;
    }).join(" ");
    const path = document.createElementNS(svgNS, "path");
    Object.entries({ d, fill: "none", stroke: colors[series], "stroke-width": 2, "vector-effect": "non-scaling-stroke", "stroke-dasharray": series ? "7 5" : "none" }).forEach(([key, value]) => path.setAttribute(key, value));
    element.append(path);
  });
  $("plot").replaceChildren(element);
  }
  if ($("renderer").value === "canvas") {
    const canvas = document.createElement("canvas");
    const pixelRatio = window.devicePixelRatio || 1;
    canvas.width = width * pixelRatio;
    canvas.height = height * pixelRatio;
    canvas.setAttribute("aria-hidden", "true");
    const context = canvas.getContext("2d");
    context.scale(pixelRatio, pixelRatio);
    context.strokeStyle = style.getPropertyValue("--rule").trim();
    context.fillStyle = style.color;
    context.font = "14px sans-serif";
    for (const tick of [0, 25, 50, 75, 100]) {
      context.beginPath(); context.moveTo(40, y(tick)); context.lineTo(width - 16, y(tick)); context.stroke(); context.fillText(tick, 1, y(tick) + 4);
    }
    ["fiveHour", "weekly"].forEach((field, series) => {
      context.strokeStyle = colors[series];
      context.lineWidth = 2;
      context.setLineDash(series ? [7, 5] : []);
      context.beginPath();
      let continuous = false;
      records.forEach((record, i) => {
        if (record[field] === null) { continuous = false; return; }
        if (continuous) context.lineTo(x(i), y(record[field]));
        else context.moveTo(x(i), y(record[field]));
        continuous = true;
      });
      context.stroke();
    });
    $("plot").replaceChildren(canvas);
  }
  $("measurement").textContent = `Draw work: ${(performance.now() - began).toFixed(1)} ms; ${records.length * 2} metric positions; no chart dependency. This is synchronous draw time, not end-to-end latency.`;
}

function render() {
  const locale = $("locale").value;
  dateFormat = new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short", timeZone: "UTC" });
  numberFormat = new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 0 });
  document.documentElement.dataset.theme = $("theme").value;
  const count = $("period").value === "week" ? 7 * 48 : $("period").value === "month" ? 30 * 48 : fixture.length;
  const offset = $("profile").value === "work" ? 7 : 0;
  const sampled = fixture.slice(-count).filter((_, i) => $("resolution").value === "raw" || i % 48 === 47);
  records = sampled.map((record) => ({ ...record, fiveHour: record.fiveHour === null ? null : Math.max(0, record.fiveHour - offset), weekly: record.weekly === null ? null : Math.max(0, record.weekly - offset) }));
  selected = Math.min(selected, records.length - 1);
  page = Math.floor(selected / pageSize);
  $("sample").max = records.length - 1;
  $("scope").textContent = `${$("profile").selectedOptions[0].textContent} · ${records.length.toLocaleString(locale)} displayed samples from ${count.toLocaleString(locale)} half-hour captures · ${$("resolution").selectedOptions[0].textContent} · Aug 1, 2025–Sep 1, 2026 fixture · Display and fixture bucket zone: UTC`;
  document.querySelector(".axis").firstElementChild.textContent = dateText(records[0]);
  document.querySelector(".axis").lastElementChild.textContent = dateText(records.at(-1));
  draw();
  showSelection();
}

document.querySelectorAll("select").forEach((select) => select.addEventListener("change", render));
$("sample").addEventListener("input", () => { selected = Number($("sample").value); page = Math.floor(selected / pageSize); showSelection(); });
$("previous").addEventListener("click", () => { page--; selected = page * pageSize; showSelection(); });
$("next").addEventListener("click", () => { page++; selected = page * pageSize; showSelection(); });
matchMedia("(prefers-color-scheme: dark)").addEventListener("change", render);
matchMedia("(forced-colors: active)").addEventListener("change", render);
render();

let plotWidth = 0;
new ResizeObserver(([entry]) => {
  if (entry.contentRect.width !== plotWidth) { plotWidth = entry.contentRect.width; draw(); }
}).observe($("plot"));
