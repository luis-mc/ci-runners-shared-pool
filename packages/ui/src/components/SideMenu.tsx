// Component 71: a side menu with its page. A numbered list of links at the side on a wide screen and above the page
// on a phone; the open item is marked. Links take their colours from tokens in every state (UX-80).
import type { ReactNode } from 'react';
import { NavLink } from 'react-router-dom';

export interface SideMenuItem {
  to: string;
  label: string;
}

export function SideMenu({
  label,
  items,
  children,
}: {
  label: string;
  items: readonly SideMenuItem[];
  children: ReactNode;
}) {
  return (
    <div className="ds-sidemenu-layout">
      <nav className="ds-sidemenu" aria-label={label}>
        <ol className="ds-sidemenu__list">
          {items.map((item, i) => (
            <li key={item.to}>
              <NavLink to={item.to} end className="ds-sidemenu__link">
                <span className="ds-sidemenu__number" aria-hidden="true">
                  {i + 1}
                </span>
                {item.label}
              </NavLink>
            </li>
          ))}
        </ol>
      </nav>
      <div className="ds-sidemenu-layout__body">{children}</div>
    </div>
  );
}
