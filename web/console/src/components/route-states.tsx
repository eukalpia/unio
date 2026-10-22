import { Link } from "@tanstack/react-router";
import { Button } from "./ui/button";
import { Wordmark } from "./logo";
import { describeError } from "../lib/api/errors";

export function NotFoundPage() {
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-4 p-6 text-center">
      <Wordmark />
      <h1 className="text-xl font-semibold">Page not found</h1>
      <p className="text-muted-foreground text-sm">The address does not match a page in the console.</p>
      <Button asChild>
        <Link to="/">Go to the dashboard</Link>
      </Button>
    </div>
  );
}

export function RouteErrorPage({ error, reset }: { error: unknown; reset: () => void }) {
  return (
    <div className="flex min-h-dvh flex-col items-center justify-center gap-4 p-6 text-center">
      <Wordmark />
      <h1 className="text-xl font-semibold">The console could not load this page</h1>
      <p role="alert" className="text-muted-foreground max-w-md text-sm">
        {describeError(error)}
      </p>
      <Button
        onClick={() => {
          reset();
        }}
      >
        Try again
      </Button>
    </div>
  );
}
