// Tabs over a list of named texts, with adding, renaming and removing.
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
}

export function Tabs({ names, active, flagged, onSelect, onAdd, onRename, onRemove, label, docs }: Props) {
  return (
    <div class="tabs" role="tablist" aria-label={label}>
      {names.map((n, i) => (
        <span key={i} class={`tab ${i === active ? "active" : ""}`}>
          <button
            role="tab"
            aria-selected={i === active}
            onClick={() => onSelect(i)}
            onDblClick={() => {
              if (!onRename) return;
              const name = prompt("Rename to", n);
              if (name && name !== n) onRename(i, name);
            }}
            title={onRename ? "Double-click to rename" : undefined}
          >
            {flagged?.has(n) && <span class="flag" aria-label="has diagnostics">●</span>}
            {n}
          </button>
          {docs?.[i] && <DocLink topic={docs[i]!} />}
          {onRemove && names.length > 1 && (
            <button class="close" aria-label={`Remove ${n}`} onClick={() => onRemove(i)}>
              ×
            </button>
          )}
        </span>
      ))}
      {onAdd && (
        <button class="add" aria-label={`Add to ${label}`} onClick={onAdd}>
          +
        </button>
      )}
    </div>
  );
}
