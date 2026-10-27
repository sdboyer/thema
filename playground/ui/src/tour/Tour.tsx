// The tour, as a column beside the playground: every step at once, to follow
// by hand, with the data to paste and what to expect.
import { useState } from "preact/hooks";
import { DocLink } from "../ui/docs";
import { type Snippet, type Step, steps } from "./steps";

export function Tour({ onClose }: { onClose: () => void }) {
  return (
    <aside class="tour" aria-label="Tour">
      <div class="bar">
        <h2>Tour</h2>
        <button class="close" onClick={onClose} aria-label="Close the tour">
          ×
        </button>
      </div>
      <ol class="steps">
        {steps.map((s) => (
          <StepView key={s.id} step={s} />
        ))}
      </ol>
    </aside>
  );
}

function StepView({ step }: { step: Step }) {
  return (
    <li class="step" id={`tour-${step.id}`}>
      <h3>
        {step.title}
        {step.doc && <DocLink topic={step.doc} />}
      </h3>
      <p>
        <Prose text={step.about} />
      </p>
      {step.todo.map((t, i) => (
        <p key={i} class="todo">
          <Prose text={t} />
        </p>
      ))}
      {(step.snippets ?? []).map((sn) => (
        <SnippetView key={sn.label} snippet={sn} />
      ))}
      <p class="see">
        <span class="label">You should see:</span> <Prose text={step.see} />
      </p>
      {step.result && <pre class="result-text">{step.result}</pre>}
    </li>
  );
}

function SnippetView({ snippet }: { snippet: Snippet }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(snippet.text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // No clipboard; the text is there to select.
    }
  };
  return (
    <figure class="snippet">
      <figcaption>
        {snippet.label}
        <button onClick={copy}>{copied ? "Copied" : "Copy"}</button>
      </figcaption>
      <pre>{snippet.text}</pre>
    </figure>
  );
}

// Prose renders text with `backticks` as code.
export function Prose({ text }: { text: string }) {
  return <>{text.split(/(`[^`]+`)/).map((part, i) => (part.startsWith("`") && part.endsWith("`") && part.length > 1 ? <code key={i}>{part.slice(1, -1)}</code> : part))}</>;
}
