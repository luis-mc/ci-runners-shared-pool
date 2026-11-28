// The shot guard (website T13, R5.4, R6.8).
import { describe, expect, it } from 'vitest';
import { findPrivate } from './shotcheck.ts';

const who = { hostname: 'REAL-PC', logins: ['luis'], owners: ['luis-mc'] };

describe('findPrivate', () => {
  it('accepts a demo screen', () => {
    expect(findPrivate('octo build-1 octo/app 40% of 80%', who)).toEqual([]);
  });
  it('rejects a real login, machine name, repository or token', () => {
    expect(findPrivate('signed in as luis', who)).toEqual(['the login luis']);
    expect(findPrivate('machine real-pc is online', who)[0]).toMatch(/machine name/);
    expect(findPrivate('luis-mc/ci-runners-shared-pool', who)[0]).toMatch(
      /repository owner/,
    );
    expect(findPrivate('token ghp_abcdefghijklmnopqrstuvwxyz0123456789', who)).toContain(
      'a GitHub token',
    );
    expect(findPrivate('mail me@example.com', who)).toContain('an email address');
  });
  it('does not match a login inside another word', () => {
    expect(findPrivate('luisa', who)).toEqual([]);
  });
});
