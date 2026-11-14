import { Link } from "react-router";
import { EmptyState, Panel } from "../components/ui/Panel";

export function NotFoundPage() {
  return (
    <Panel>
      <EmptyState icon="explore_off" title="Page not found" action={<Link to="/" className="text-title-md underline">Go to overview</Link>}>
        The page you requested does not exist.
      </EmptyState>
    </Panel>
  );
}
