import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { App } from './App';
import { RouteError } from './screens/common';
import '@crsp/ui/ui.css';

const container = document.getElementById('root');
if (!container) throw new Error('#root is missing from index.html');

// A data router, so a screen with unsaved changes can hold a move out of it (LOOK21, `LeaveGuard`).
const router = createBrowserRouter([{ path: '*', element: <App />, errorElement: <RouteError /> }]);

createRoot(container).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
