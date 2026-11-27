import { configure } from '@testing-library/react';

// Every findBy and waitFor waits up to 5 s: a slow shared runner must not fail a
// test that only waits for a screen to settle (owner decision D105).
configure({ asyncUtilTimeout: 5000 });
