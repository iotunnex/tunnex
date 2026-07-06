import type { ReactNode } from "react";
import { Logo, PRODUCT_TAGLINE } from "../brand";
import { Card } from "./ui";
import { HealthStatus } from "./HealthStatus";

/** AuthLayout is the shared frame for the pre-auth screens (login, signup, reset,
 * verify): centered brand + card, health/tagline footer. */
export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-full flex-col">
      <main className="grid flex-1 place-items-center px-6 py-10">
        <div className="w-full max-w-sm">
          <div className="mb-6 flex justify-center">
            <Logo />
          </div>
          <Card>{children}</Card>
        </div>
      </main>
      <footer className="flex items-center justify-between px-6 py-4 text-xs text-slate-600">
        <HealthStatus />
        <span>{PRODUCT_TAGLINE}</span>
      </footer>
    </div>
  );
}
