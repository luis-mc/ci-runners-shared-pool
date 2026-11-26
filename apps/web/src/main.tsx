import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { App } from './App';
import { RouteError } from './screens/common';
import '@crsp/ui/ui.css';

const container = document.getElementById('root');
if (!container) throw new Error('#root is missing from index.html');

// A data router, so a screen with unsaved changes can hold a move out of it (LOOK21, `LeaveGuard`).
const router = createBrowserRouter([
  { path: '*', element: <App />, errorElement: <RouteError /> },
]);

// TEMPORARY (CRSP338): a dev server started with VITE_CRSP338_DEMO=1 runs on a fake controller (demo data).
class NoStream {
  onopen = null;
  onerror = null;
  onmessage = null;
  addEventListener() {}
  close() {}
}
let shown = router;
if (import.meta.env.VITE_CRSP338_DEMO) {
  const { demoApi } = await import('./screens/repositories/options/demoApi');
  const demo = demoApi(window.location.search);
  shown = createBrowserRouter([
    {
      path: '*',
      element: <App {...demo} eventSource={() => new NoStream()} />,
      errorElement: <RouteError />,
    },
  ]);
}

createRoot(container).render(
  <StrictMode>
    <RouterProvider router={shown} />
  </StrictMode>,
);
