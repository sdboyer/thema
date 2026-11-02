// The request each user operation sends, from the session.
import {
  type CodegenOp,
  type CodegenRequest,
  type DataOp,
  type HydrateRequest,
  type TranslateRequest,
  type ValidateAnyRequest,
  type ValidateRequest,
  isCodegen,
} from "@sdboyer/thema-ui";
import { type Session, dataFor, lineageInput } from "./session";

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
