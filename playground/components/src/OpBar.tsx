// The operations bar: a button per operation, the arguments the chosen one
// takes, and Run. The host holds the state; the bar only shows and changes it.
import { DocLink, hasDoc } from "./docs.js";
import { type OpArgs, type UserOp, argsOf, codegenOps, dataOps } from "./ops.js";

export interface OpBarProps {
  op: UserOp;
  args: OpArgs;
  // versions are the lineage's, and label how each reads.
  versions: readonly string[];
  label: (version: string) => string;
  onOp: (op: UserOp) => void;
  onArgs: (args: OpArgs) => void;
  onRun: () => void;
  running: boolean;
  // disabled is why Run can't be pressed, as its title; empty when it can.
  disabled?: string;
  // ops are the operations offered; all of them when absent.
  ops?: readonly UserOp[];
}

export function OpBar({ op, args, versions, label, onOp, onArgs, onRun, running, disabled, ops }: OpBarProps) {
  const offered = (o: UserOp) => !ops || ops.includes(o);
  const setArg = (k: keyof OpArgs, v: string) => onArgs({ ...args, [k]: v });
  const versionSelect = (k: "version" | "from" | "to", none: string) => (
    <select value={args[k]} onChange={(e) => setArg(k, (e.target as HTMLSelectElement).value)} aria-label={k}>
      <option value="">{none}</option>
      {versions.map((v) => (
        <option key={v} value={v}>
          {label(v)}
        </option>
      ))}
    </select>
  );
  const takes = argsOf(op);
  const opButton = (o: UserOp) => (
    <span key={o} class="op">
      <button aria-pressed={op === o} onClick={() => onOp(o)}>
        {o}
      </button>
      {hasDoc(o) && <DocLink topic={o} />}
    </span>
  );
  const onData = dataOps.filter(offered);
  const codegen = codegenOps.filter(offered);
  return (
    <div class="ops">
      <div class="bar" role="toolbar" aria-label="Operations">
        {onData.length > 0 && (
          <span class="op-group" role="group" aria-label="On data">
            {onData.map(opButton)}
          </span>
        )}
        {codegen.length > 0 && (
          <span class="op-group" role="group" aria-label="Code generation">
            {codegen.map(opButton)}
          </span>
        )}
      </div>
      <div class="bar op-args">
        {takes.includes("version") && <label>schema {versionSelect("version", "latest")}</label>}
        {takes.includes("from") && <label>from {versionSelect("from", "the data's")}</label>}
        {takes.includes("to") && <label>to {versionSelect("to", "choose")}</label>}
        {takes.includes("format") && (
          <label>
            as{" "}
            <select value={args.format} onChange={(e) => setArg("format", (e.target as HTMLSelectElement).value)} aria-label="format">
              <option value="">{op === "openapi" ? "YAML" : "JSON"}</option>
              <option value="json">JSON</option>
              <option value="yaml">YAML</option>
            </select>
          </label>
        )}
        <button class="primary" onClick={onRun} disabled={!!disabled || running} title={disabled || undefined}>
          {running ? "Running…" : "Run"}
        </button>
      </div>
    </div>
  );
}
