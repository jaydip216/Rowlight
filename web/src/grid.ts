import type { CellValue, EncodedCell, ResultColumn } from "./types";

const ROW_HEIGHT = 31;
const OVERSCAN = 8;

export class ResultGrid {
  private columns: ResultColumn[] = [];
  private rows: CellValue[][] = [];
  private viewport: HTMLDivElement;
  private header: HTMLDivElement;
  private spacer: HTMLDivElement;
  private layer: HTMLDivElement;

  constructor(private root: HTMLElement) {
    root.innerHTML = `
      <div class="grid-header" role="row"></div>
      <div class="grid-viewport" tabindex="0" aria-label="Query results">
        <div class="grid-spacer"></div><div class="grid-layer"></div>
      </div>`;
    this.header = root.querySelector(".grid-header")!;
    this.viewport = root.querySelector(".grid-viewport")!;
    this.spacer = root.querySelector(".grid-spacer")!;
    this.layer = root.querySelector(".grid-layer")!;
    this.viewport.addEventListener("scroll", () => this.renderRows());
    new ResizeObserver(() => this.renderRows()).observe(this.viewport);
  }

  reset(columns: ResultColumn[] = []): void {
    this.columns = columns;
    this.rows = [];
    this.viewport.scrollTop = 0;
    this.renderHeader();
    this.renderRows();
  }

  setColumns(columns: ResultColumn[]): void {
    this.columns = columns;
    this.renderHeader();
  }

  append(rows: CellValue[][]): void {
    this.rows.push(...rows);
    this.renderRows();
  }

  get rowCount(): number { return this.rows.length; }

  toCSV(): string {
    const lines = [this.columns.map((column) => csvCell(column.name)).join(",")];
    for (const row of this.rows) lines.push(row.map((value) => csvCell(exportValue(value))).join(","));
    return `${lines.join("\r\n")}\r\n`;
  }

  toTSV(): string {
    const lines = [this.columns.map((column) => tsvCell(column.name)).join("\t")];
    for (const row of this.rows) lines.push(row.map((value) => tsvCell(exportValue(value))).join("\t"));
    return lines.join("\n");
  }

  private gridTemplate(): string {
    return `52px repeat(${Math.max(this.columns.length, 1)}, minmax(150px, 1fr))`;
  }

  private renderHeader(): void {
    this.header.replaceChildren();
    this.header.style.gridTemplateColumns = this.gridTemplate();
    this.header.append(this.cell("#", "grid-cell row-number"));
    for (const column of this.columns) {
      const cell = this.cell(column.name, "grid-cell column-header");
      cell.title = `${column.name} · ${column.databaseType}`;
      this.header.append(cell);
    }
  }

  private renderRows(): void {
    this.spacer.style.height = `${this.rows.length * ROW_HEIGHT}px`;
    if (!this.rows.length) {
      this.layer.replaceChildren();
      return;
    }
    const first = Math.max(0, Math.floor(this.viewport.scrollTop / ROW_HEIGHT) - OVERSCAN);
    const count = Math.ceil(this.viewport.clientHeight / ROW_HEIGHT) + OVERSCAN * 2;
    const last = Math.min(this.rows.length, first + count);
    const fragment = document.createDocumentFragment();
    for (let index = first; index < last; index += 1) {
      const row = this.rows[index];
      if (!row) continue;
      const element = document.createElement("div");
      element.className = "grid-row";
      element.setAttribute("role", "row");
      element.style.top = `${index * ROW_HEIGHT}px`;
      element.style.gridTemplateColumns = this.gridTemplate();
      element.append(this.cell(String(index + 1), "grid-cell row-number"));
      for (const value of row) element.append(this.valueCell(value));
      fragment.append(element);
    }
    this.layer.replaceChildren(fragment);
  }

  private valueCell(value: CellValue): HTMLDivElement {
    const display = value === null ? "NULL" : typeof value === "string" ? value : encodedCellText(value);
    const element = this.cell(display, "grid-cell");
    if (value === null) element.classList.add("is-null");
    if (typeof value === "object" && value?.truncated) element.classList.add("is-truncated");
    element.title = display;
    return element;
  }

  private cell(text: string, className: string): HTMLDivElement {
    const element = document.createElement("div");
    element.className = className;
    element.textContent = text;
    element.setAttribute("role", "gridcell");
    return element;
  }
}

function encodedCellText(value: EncodedCell): string {
  if (value.encoding === "base64") {
    return `[binary · base64] ${value.data}${value.truncated ? "…" : ""}`;
  }
  return `${value.data}${value.truncated ? "…" : ""}`;
}

function exportValue(value: CellValue): string {
  if (value === null) return "\\N";
  if (typeof value === "string") return value;
  const prefix = value.encoding === "base64" ? "base64:" : "";
  return `${value.truncated ? "[truncated]" : ""}${prefix}${value.data}`;
}

function csvCell(value: string): string {
  return `"${value.replaceAll('"', '""')}"`;
}

function tsvCell(value: string): string {
  return value.replaceAll("\\", "\\\\").replaceAll("\t", "\\t").replaceAll("\r", "\\r").replaceAll("\n", "\\n");
}
