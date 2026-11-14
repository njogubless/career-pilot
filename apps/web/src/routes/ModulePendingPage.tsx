import { Link } from "react-router";
import { Icon } from "../components/ui/Icon";
import { EmptyState, Panel } from "../components/ui/Panel";
import { Tag } from "../components/ui/Tag";
import type { NavItem } from "../components/shell/nav";

/** Honest state for modules on the roadmap but not yet built (master prompt §31). */
export function ModulePendingPage({ item }: { item: NavItem }) {
  return (
    <div className="flex flex-col gap-space-lg">
      <header className="flex flex-col gap-space-xs">
        <div className="flex items-center gap-space-xs">
          <Tag tone="outline">Roadmap · Phase {item.phase}</Tag>
        </div>
        <h1 className="font-serif text-headline-lg-mobile text-on-surface md:text-headline-lg">{item.title}</h1>
        <p className="max-w-2xl text-body-lg text-on-surface-variant">{item.summary}</p>
      </header>
      <Panel>
        <EmptyState
          icon={item.icon}
          title="Not available yet"
          action={
            <Link to="/" className="inline-flex items-center gap-1 text-title-md text-on-surface underline-offset-4 hover:underline">
              <Icon name="arrow_back" size={16} /> Back to overview
            </Link>
          }
        >
          This module is scheduled for phase {item.phase} of the development roadmap. It will appear here once its backend
          and tests are in place. Nothing on this page is simulated.
        </EmptyState>
      </Panel>
    </div>
  );
}
