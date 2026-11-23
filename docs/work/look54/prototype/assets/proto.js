// LOOK54 prototype: small inline behaviour. Everything else is plain links. No network.
(function () {
  var $ = function (s, r) { return (r || document).querySelector(s); };
  var $$ = function (s, r) { return Array.prototype.slice.call((r || document).querySelectorAll(s)); };
  // The narrow menu: one Menu button that opens the list, as in the console.
  var tg = $('[data-menu-toggle] button'), nav = $('.ds-topmenu');
  if (tg && nav) {
    var here = tg.getAttribute('data-here');
    tg.addEventListener('click', function () {
      var o = nav.classList.toggle('open');
      tg.textContent = o ? 'Close the menu' : 'Menu: ' + here;
      tg.setAttribute('aria-expanded', String(o));
    });
  }
  // Segmented filters: one pressed at a time.
  $$('.ds-segmented').forEach(function (g) {
    g.addEventListener('click', function (e) {
      var b = e.target.closest('button'); if (!b) return;
      $$('button', g).forEach(function (x) { x.setAttribute('aria-pressed', String(x === b)); });
    });
  });
  // Pool > Jobs: the why-waiting inspector and the filters.
  var grid = $('.pv-jobs');
  if (grid) {
    var show = function (id) {
      var found = false;
      $$('aside[data-job]', grid).forEach(function (a) { var on = a.getAttribute('data-job') === id; a.hidden = !on; found = found || on; });
      $$('tr[data-job]', grid).forEach(function (r) { if (r.getAttribute('data-job') === id) r.setAttribute('aria-selected', 'true'); else r.removeAttribute('aria-selected'); });
      grid.classList.toggle('insp', found);
    };
    grid.addEventListener('click', function (e) {
      var w = e.target.closest('[data-why]');
      if (w) { show(w.getAttribute('data-why')); var a = $('aside[data-job]:not([hidden])', grid); if (a && a.scrollIntoView) a.scrollIntoView({ block: 'nearest' }); return; }
      if (e.target.closest('[data-close]')) { show(''); return; }
      var f = e.target.closest('[data-filters]');
      if (f) {
        var body = $('[data-filters-body]', grid);
        var open = body.classList.contains('pv-fbody-closed');
        body.classList.toggle('pv-fbody-closed', !open);
        body.classList.toggle('pv-fbody-shown', open);
        f.textContent = open ? 'Close the filters' : 'Open the filters';
      }
    });
    if (location.hash.length > 1) show(decodeURIComponent(location.hash.slice(1)));
  }
})();
