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

// TEMPORARY (CRSP342, CRSP343): design option screens for review. Delete with apps/web/src/temp; never merge.
const q = new URLSearchParams(window.location.search);
const option = q.get('option');
const task = q.get('task');
const root = createRoot(container);
if (task && option && import.meta.env.DEV) {
  void import('./temp/DesignOptions').then((m) =>
    root.render(<m.DesignOptions task={task} option={option} />),
  );
} else {
  root.render(
    <StrictMode>
      <RouterProvider router={router} />
    </StrictMode>,
  );
}
