// Runs before the first paint (a classic script in <head>): puts the saved theme name and mode on the page,
// so there is no flash of the default theme. The names match packages/ui/src/themes.ts.
(function () {
  try {
    var root = document.documentElement;
    var names = [
      'phosphor',
      'tokyo-night',
      'catppuccin',
      'gruvbox',
      'solarized',
      'nord',
      'terminal',
    ];
    var name = window.localStorage.getItem('crsp.theme-name');
    // A new install is Phosphor, dark (owner decision LOOK D11); a saved 'system' leaves the mode to the OS.
    root.setAttribute('data-theme-name', names.indexOf(name) >= 0 ? name : 'phosphor');
    var pal = window.localStorage.getItem('crsp.theme-palette');
    if (pal === 'amber' || pal === 'ice') root.setAttribute('data-pal', pal);
    if (window.localStorage.getItem('crsp.theme-graphs') === 'dotted')
      root.setAttribute('data-graphs', 'dotted');
    var mode = window.localStorage.getItem('crsp.theme-mode');
    // Nothing saved yet: a new install is dark (owner decision D4). Only a saved "system" follows the device.
    if (mode === 'dark' || mode === 'light') root.setAttribute('data-theme', mode);
    else if (mode !== 'system') root.setAttribute('data-theme', 'dark');
  } catch (e) {
    // Storage is blocked: the page keeps the default theme.
    document.documentElement.setAttribute('data-theme-name', 'phosphor');
    document.documentElement.setAttribute('data-theme', 'dark');
  }
})();
