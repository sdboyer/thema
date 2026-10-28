// The request each user operation sends, from the session.
import type { CodegenRequest, HydrateRequest, TranslateRequest, ValidateAnyRequest, ValidateRequest } from "../api/types";
import { type CodegenOp, type DataOp, type Session, dataFor, lineageInput } from "./session";

export type UserRequest =
  | { op: "validate"; req: ValidateRequest }
  | { op: "validate-any"; req: ValidateAnyRequest }
  | { op: "translate"; req: TranslateRequest }
  | { op: "hydrate" | "dehydrate"; req: HydrateRequest }
  | { op: CodegenOp; req: CodegenRequest };

// requestFor is the request s.op sends, or a sentence saying what it lacks.
export function requestFor(s: Session): UserRequest | string {
  const lineage = lineageInput(s);
  const v = (x: string) => x || undefined;
  if (isCodegen(s.op)) {
    return { op: s.op, req: { lineage, version: v(s.args.version), format: s.op === "typescript" ? undefined : v(s.args.format) } };
  }
  const data = dataFor(s);
  if (!data) return "add a data document to run this";
  const op: DataOp = s.op;
  switch (op) {
    case "validate":
      return { op, req: { lineage, data, version: v(s.args.version) } };
    case "validate-any":
      return { op, req: { lineage, data } };
    case "translate":
      if (!s.args.to) return "choose the version to translate to";
      return { op, req: { lineage, data, from: v(s.args.from), to: s.args.to } };
    case "hydrate":
    case "dehydrate":
      return { op, req: { lineage, data, version: v(s.args.version) } };
  }
}

export function isCodegen(op: string): op is CodegenOp {
  return op === "openapi" || op === "jsonschema" || op === "typescript";
}

// The arguments each operation takes, for the panel to offer.
export function argsOf(op: Session["op"]): ReadonlyArray<"version" | "from" | "to" | "format"> {
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
