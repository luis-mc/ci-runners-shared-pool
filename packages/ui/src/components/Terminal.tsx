// Component 82: the terminal of a running step (UX-87). Command lines, their output as it arrives (standard output
// and error told apart) and the machine that runs them, per block. A page passes `ProgressEvent` rows only.
// The row type is structural: it is the one of `@crsp/contract` without importing it.
import { useEffect, useId, useRef, useState } from 'react';
import { Button } from './Button';
import { formatTime } from './format';

export interface TerminalRow {
  machine: { name: string; role: 'controller' | 'agent'; os: string };
  command?: string | undefined;
  stream: 'stdout' | 'stderr';
  line?: string | undefined;
  ts: number;
}

interface Block {
  key: string;
  machine: TerminalRow['machine'];
  command?: string | undefined;
  ts: number;
  lines: { text: string; error: boolean; key: string }[];
}

/** A block starts at a new command or when the machine changes. Rows with neither a command nor a line are skipped. */
export function terminalBlocks(rows: readonly TerminalRow[]): Block[] {
  const out: Block[] = [];
  rows.forEach((r, i) => {
    const last = out[out.length - 1];
    if (r.command !== undefined || !last || last.machine.name !== r.machine.name) {
      if (r.command === undefined && r.line === undefined) return;
      out.push({
        key: `b${i}`,
        machine: r.machine,
        command: r.command,
        ts: r.ts,
        lines: [],
      });
    }
    const block = out[out.length - 1];
    if (block && r.line !== undefined)
      block.lines.push({ text: r.line, error: r.stream === 'stderr', key: `l${i}` });
  });
  return out;
}

export function Terminal({
  rows,
  open,
  onToggle,
}: {
  rows: readonly TerminalRow[];
  /** Open shows the lines; closed shows the last command and Show log. */
  open: boolean;
  onToggle: (open: boolean) => void;
}) {
  const id = useId();
  const box = useRef<HTMLDivElement>(null);
  const [follow, setFollow] = useState(true);
  const blocks = terminalBlocks(rows);
  const lastCommand = [...blocks].reverse().find((b) => b.command)?.command;
  useEffect(() => {
    const el = box.current;
    if (open && follow && el) el.scrollTop = el.scrollHeight;
  }, [rows.length, open, follow]);
  return (
    <section
      className="ds-terminal"
      aria-label="Command log"
      data-open={open || undefined}
    >
      <div className="ds-terminal__bar">
        <span className="ds-terminal__title">Command log</span>
        {!open && lastCommand ? (
          <code className="ds-code">{`$ ${lastCommand}`}</code>
        ) : null}
        <Button expanded={open} controls={id} onPress={() => onToggle(!open)}>
          {open ? 'Hide log' : 'Show log'}
        </Button>
      </div>
      {open ? (
        <div
          id={id}
          ref={box}
          className="ds-terminal__body"
          role="log"
          aria-label="Command log"
          tabIndex={0}
          onScroll={(e) => {
            const el = e.currentTarget;
            setFollow(el.scrollHeight - el.scrollTop - el.clientHeight < 8);
          }}
        >
          {blocks.length === 0 ? <p className="ds-text">No commands yet.</p> : null}
          {blocks.map((b) => (
            <div key={b.key} className="ds-terminal__block">
              <p className="ds-terminal__head">
                <strong>{b.machine.name}</strong>
                <span className="ds-tag" data-role={b.machine.role}>
                  {b.machine.role}
                </span>
                <span>{b.machine.os}</span>
                <span>{formatTime(b.ts)}</span>
              </p>
              {b.command ? (
                <code className="ds-code ds-terminal__cmd">{`$ ${b.command}`}</code>
              ) : null}
              {b.lines.map((l) => (
                <code
                  key={l.key}
                  className="ds-code ds-terminal__line"
                  data-error={l.error || undefined}
                >
                  {l.error ? <span className="ds-terminal__mark">error </span> : null}
                  {l.text}
                </code>
              ))}
            </div>
          ))}
        </div>
      ) : null}
      {open && !follow ? (
        <Button
          onPress={() => {
            setFollow(true);
          }}
        >
          Jump to latest
        </Button>
      ) : null}
    </section>
  );
}
