// Tabs over a list of named texts, with adding, renaming and removing. Frozen
// tabs are grouped after the others and can be opened but never renamed or
// removed.
import { DocLink, type Topic } from "./docs";

interface Props {
  names: readonly string[];
  active: number;
  // flagged names get a mark: the files diagnostics point into.
  flagged?: Set<string>;
  onSelect: (i: number) => void;
  onAdd?: () => void;
  onRename?: (i: number, name: string) => void;
  onRemove?: (i: number) => void;
  label: string;
  // docs links a tab's name, where it names a term, to its reference entry.
  docs?: readonly (Topic | undefined)[];
  // frozen names the tabs that are frozen, and frozenLabel their group.
  frozen?: (name: string) => boolean;
  frozenLabel?: string;
}

export function Tabs({ names, active, flagged, onSelect, onAdd, onRename, onRemove, label, docs, frozen, frozenLabel }: Props) {
  const isFrozen = (n: string) => !!frozen?.(n);
  const tab = (n: string, i: number) => {
    const fixed = isFrozen(n);
    const rename = fixed ? undefined : onRename;
    return (
      <span key={i} class={`tab ${i === active ? "active" : ""} ${fixed ? "frozen" : ""}`}>
        <button
          role="tab"
          aria-selected={i === active}
          onClick={() => onSelect(i)}
          onDblClick={() => {
            if (!rename) return;
            const name = prompt("Rename to", n);
            if (name && name !== n) rename(i, name);
          }}
          title={fixed ? "Frozen: this version is cut and never changes" : rename ? "Double-click to rename" : undefined}
        >
          {flagged?.has(n) && <span class="flag" aria-label="has diagnostics">●</span>}
          {n}
        </button>
        {docs?.[i] && <DocLink topic={docs[i]!} />}
        {onRemove && !fixed && names.length > 1 && (
          <button class="close" aria-label={`Remove ${n}`} onClick={() => onRemove(i)}>
            ×
          </button>
        )}
      </span>
    );
  };
  const indexed = names.map((n, i) => [n, i] as const);
  const open = indexed.filter(([n]) => !isFrozen(n));
  const fixed = indexed.filter(([n]) => isFrozen(n));
  return (
    <div class="tabs" role="tablist" aria-label={label}>
      {open.map(([n, i]) => tab(n, i))}
      {onAdd && (
        <button class="add" aria-label={`Add to ${label}`} onClick={onAdd}>
          +
        </button>
      )}
      {fixed.length > 0 && (
        <span class="tab-group frozen" role="group" aria-label={frozenLabel}>
          <span class="group-label" aria-hidden="true">
            {frozenLabel}
          </span>
          {fixed.map(([n, i]) => tab(n, i))}
        </span>
      )}
    </div>
  );
}
