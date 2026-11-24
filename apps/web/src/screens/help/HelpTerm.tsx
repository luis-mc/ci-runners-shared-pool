// The ? beside a term in the console (LOOK44): the shared term-help part with the words of Help.
import { TermHelp } from '@crsp/ui';
import { TERMS, type TermKey } from './helpContent';

export function HelpTerm({ term }: { term: TermKey }) {
  const t = TERMS[term];
  return <TermHelp term={t.term} answer={t.answer} to={t.to} />;
}
