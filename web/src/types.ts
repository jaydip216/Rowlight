export type TLSMode = "disabled" | "system" | "custom" | "mutual";
export type DatabaseEngine = "mysql" | "postgres";

export interface ConnectionInput {
  engine: DatabaseEngine;
  host: string;
  port: number;
  user: string;
  password: string;
  database: string;
  tls: {
    mode: TLSMode;
    serverName?: string;
    caPem?: string;
    clientCertPem?: string;
    clientKeyPem?: string;
  };
}

export interface Connection {
  id: string;
  engine?: DatabaseEngine;
  serverVersion: string;
  database: string;
}

export interface ConnectionProfile {
  id: string;
  name: string;
  // Optional so profiles written before PostgreSQL support still load as MySQL.
  engine?: DatabaseEngine;
  host: string;
  port: number;
  user: string;
  database: string;
  tls: {
    mode: TLSMode;
    serverName?: string;
    caPem?: string;
    clientCertPem?: string;
  };
  updatedAt: string;
}

export interface QueryHistoryEntry {
  id: string;
  sql: string;
  engine?: DatabaseEngine;
  database: string;
  server: string;
  executedAt: string;
  elapsedMs: number;
  rowCount: number;
  truncated: boolean;
  error?: string;
}

export type BrowseFilterOperator = "equals" | "contains" | "startsWith" | "isNull" | "isNotNull";

export interface BrowseRequest {
  schema: string;
  table: string;
  offset: number;
  pageSize: number;
  sort?: { column: string; direction: "asc" | "desc" };
  filters?: Array<{ column: string; operator: BrowseFilterOperator; value?: string }>;
}

export interface BrowseResult {
  columns: ResultColumn[];
  rows: CellValue[][];
  offset: number;
  pageSize: number;
  hasMore: boolean;
}

export interface DatabaseItem { name: string }
export interface TableItem { name: string; type: "table" | "view" | "foreign table" }
export interface ColumnItem { name: string; dataType: string; nullable: boolean; key?: string }

export interface ResultColumn {
  name: string;
  databaseType: string;
}

export interface EncodedCell {
  encoding: "base64" | "utf8";
  data: string;
  truncated: boolean;
}

export type CellValue = string | null | EncodedCell;

export type QueryEvent =
  | { type: "started"; queryId: string }
  | { type: "meta"; queryId: string; columns: ResultColumn[] }
  | { type: "rows"; rows: CellValue[][] }
  | { type: "complete"; rowCount: number; elapsedMs: number; truncated: boolean }
  | { type: "cancelled"; rowCount: number; elapsedMs: number }
  | { type: "error"; error: string };
