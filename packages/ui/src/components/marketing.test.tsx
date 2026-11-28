// Components 86 to 91 (website R4, R6.2, R6.3, R6.4): the marketing frame reads without script and has its words.
import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import { DocSearch, FeatureBand, Hero, Prose, Shot, SiteFrame } from './Marketing';

afterEach(cleanup);

describe('marketing parts', () => {
  it('the frame has a skip link, a menu, one main and a footer', () => {
    render(
      <SiteFrame menu={<nav aria-label="Site" />} footer={<p>Footer</p>}>
        <p>Body</p>
      </SiteFrame>,
    );
    expect(screen.getByRole('link', { name: 'Skip to the content' })).toBeTruthy();
    expect(screen.getAllByRole('main')).toHaveLength(1);
    expect(screen.getByRole('contentinfo').textContent).toBe('Footer');
  });

  it('the hero has the one h1, the lead and the action', () => {
    render(<Hero title="Name" lead="One line." action={<button>Go</button>} />);
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Name');
    expect(screen.getByText('One line.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Go' })).toBeTruthy();
  });

  it('a band has a level-2 heading and marks a picture beside it', () => {
    const { container } = render(
      <FeatureBand title="Band" media={<span>pic</span>}>
        <p>Text</p>
      </FeatureBand>,
    );
    expect(screen.getByRole('heading', { level: 2 }).textContent).toBe('Band');
    expect(container.querySelector('[data-media]')).toBeTruthy();
  });

  it('a shot always has alt text and its size', () => {
    render(<Shot src="/a.png" alt="The pool overview" width={800} height={450} />);
    const img = screen.getByRole('img', { name: 'The pool overview' });
    expect(img.getAttribute('width')).toBe('800');
    expect(img.getAttribute('height')).toBe('450');
  });

  it('prose draws the given html', () => {
    const { container } = render(<Prose html="<h2>Title</h2><p>Text</p>" />);
    expect(container.querySelector('.ds-prose h2')?.textContent).toBe('Title');
  });

  it('the doc search starts hidden (it needs script) and keeps its list', () => {
    const { container } = render(
      <MemoryRouter>
        <DocSearch
          label="Search the docs"
          hint="Search"
          groups={[{ title: 'Guides', items: [{ to: '/docs/guides/a', title: 'One' }] }]}
        />
      </MemoryRouter>,
    );
    expect(container.querySelector('[role="search"]')?.hasAttribute('hidden')).toBe(true);
    expect(screen.getByRole('link', { name: 'One' }).getAttribute('href')).toBe(
      '/docs/guides/a',
    );
    expect(container.querySelector('[data-doc-path="/docs/guides/a"]')).toBeTruthy();
  });
});
