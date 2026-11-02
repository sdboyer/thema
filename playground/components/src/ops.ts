// The operations a user runs on data or on a lineage, and the arguments each
// takes: what OpBar offers and Output renders the result of.

export type DataOp = "validate" | "validate-any" | "translate" | "hydrate" | "dehydrate";
export type CodegenOp = "openapi" | "jsonschema" | "typescript";
export type UserOp = DataOp | CodegenOp;

export const dataOps: readonly DataOp[] = ["validate", "validate-any", "translate", "hydrate", "dehydrate"];
export const codegenOps: readonly CodegenOp[] = ["openapi", "jsonschema", "typescript"];

export interface OpArgs {
  // version is the schema for validate, hydrate, dehydrate and code
  // generation; empty means the latest, or for hydrate the one that accepts
  // the data.
  version: string;
  from: string;
  to: string;
  format: string;
}

export function isCodegen(op: string): op is CodegenOp {
  return op === "openapi" || op === "jsonschema" || op === "typescript";
}

// The arguments each operation takes, for the panel to offer.
export function argsOf(op: UserOp): ReadonlyArray<"version" | "from" | "to" | "format"> {
  switch (op) {
    case "validate":
    case "hydrate":
    case "dehydrate":
    case "typescript":
      return ["version"];
    case "validate-any":
      return [];
    case "translate":
      return ["from", "to"];
    case "openapi":
    case "jsonschema":
      return ["version", "format"];
  }
}
