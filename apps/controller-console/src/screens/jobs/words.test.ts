// R5.115 (OPS34): the words of the Jobs section: one number in a table, a range with its basis on a page, "not
// enough history yet" with a labelled stand-in, the start range, and the wait reasons and results in words.
import { describe, expect, it } from 'vitest';
import {
  basisWords,
  forecastOf,
  forecastOne,
  forecastWords,
  insideRange,
  jobStatus,
  median,
  waitShort,
  workflowFile,
  oneNumber,
  ordinal,
  startsWords,
  taskIdOf,
  waitWords,
  type Forecast,
} from './words';

const f = (basis: Forecast['basis'], samples = 20): Forecast => ({
  p50: 420,
  lo: 360,
  hi: 540,
  basis,
  samples,
});

describe('words of the Jobs section', () => {
  it('says one number in a table and a range on a page', () => {
    expect(forecastOne(f('job_key'))).toBe('about 7 min');
    expect(forecastWords(f('job_key'))).toBe('about 6 to 9 min');
  });

  it('labels the typical time under 5 runs of the job and makes up nothing', () => {
    expect(forecastOne(f('workflow'))).toBe(
      'not enough history yet (workflow typical about 7 min)',
    );
    expect(forecastWords(f('repository'))).toContain("the repository's typical time");
    expect(forecastOne(f('not_enough_history'))).toBe('not enough history yet');
    expect(forecastWords(forecastOf({}))).toBe('not enough history yet');
  });

  it('names the basis of a forecast', () => {
    expect(basisWords({ ...f('job_key'), machine: 'build-1' })).toBe(
      'median of the last 20 runs on build-1',
    );
    expect(basisWords({ ...f('job_key'), changed_lately: true })).toContain(
      'the last 10 runs count',
    );
    expect(basisWords(f('not_enough_history', 3))).toBe(
      '3 of 5 runs needed, so no number is made up',
    );
  });

  it('rounds one number for reading', () => {
    expect(oneNumber(41)).toBe('about 40 s');
    expect(oneNumber(66)).toBe('about 70 s');
    expect(oneNumber(3900)).toBe('about 1 h 5 min');
  });

  it('says the place in line and the start range', () => {
    expect(ordinal(3)).toBe('3rd');
    expect(ordinal(12)).toBe('12th');
    expect(startsWords({ p50: 300, lo: 240, hi: 420 }, 3)).toBe(
      'starts in about 4 to 7 min, 3rd in line',
    );
    expect(startsWords({ p50: 5, lo: 0, hi: 10 }, undefined)).toBe('starts now');
    expect(startsWords(undefined, 2)).toBe('start not known');
  });

  it('finds a task id at the start of a branch', () => {
    expect(taskIdOf('OPS34-jobs-section')).toBe('OPS34');
    expect(taskIdOf('main')).toBeUndefined();
    expect(taskIdOf('feature/series-grouping')).toBeUndefined();
  });

  it('says why a queued job waits (UX-29), preferring the server line', () => {
    expect(waitWords({ wait_reason: 'free_slot', wait_text: 'waits on box-1' })).toBe(
      'waits on box-1',
    );
    expect(waitWords({ wait_reason: 'no_runner_with_labels' })).toBe(
      'waits for a runner with its labels',
    );
    expect(waitWords({})).toBe('waits to be placed');
  });

  it('gives every result a word and a glyph kind (UX-58)', () => {
    expect(jobStatus({ state: 'done', result: 'failure' })).toEqual({
      kind: 'failed',
      word: 'failure',
    });
    expect(jobStatus({ state: 'done', result: 'timed_out' }).word).toBe('timed out');
    expect(jobStatus({ state: 'queued' }).kind).toBe('waiting');
    expect(jobStatus({ state: 'running' }).kind).toBe('running');
  });

  it('takes the median and the share inside the range', () => {
    expect(median([3, 1, 2])).toBe(2);
    expect(median([1, 2, 3, 4])).toBe(2.5);
    expect(median([])).toBe(0);
    expect(
      insideRange([
        { actual: 5, lo: 4, hi: 6 },
        { actual: 9, lo: 4, hi: 6 },
      ]),
    ).toBe(0.5);
    expect(insideRange([])).toBe(0);
  });
});

describe('one-line row values (CRSP210)', () => {
  it('shows the workflow file name of a path, and the path of no folder whole', () => {
    expect(workflowFile('.github/workflows/ci.yml')).toBe('ci.yml');
    expect(workflowFile('ci.yml')).toBe('ci.yml');
    expect(workflowFile('')).toBe('');
  });
  it('gives a short wait reason from the reason, and none for a bare server line', () => {
    expect(waitShort({ wait_reason: 'free_slot' })).toBe('waits for a slot');
    expect(waitShort({ wait_text: 'waits on build-1' })).toBe(undefined);
    expect(waitShort({})).toBe('waits to be placed');
  });
});
