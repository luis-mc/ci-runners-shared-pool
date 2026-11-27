// Runs before the first paint (a classic script in <head>): the site follows the visitor's light or dark setting.
// It reads no storage and sets no cookie (website R6.5). The names match packages/ui/src/themes.ts.
(function () {
  var root = document.documentElement;
  root.setAttribute('data-theme-name', 'phosphor');
  root.setAttribute(
    'data-theme',
    window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches
      ? 'light'
      : 'dark',
  );
})();
