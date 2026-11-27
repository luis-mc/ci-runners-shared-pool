// Poll intervals of the wizards' work steps and of a wipe that is running. A test shortens them; the console
// never changes them.
export const TIMING = {
  joinPollMs: 2000,
  planPollMs: 1000,
  dependencyPollMs: 1000,
  cachePollMs: 3000,
  restartLimitMs: 5 * 60_000,
};
