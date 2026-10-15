import "./globals.css";
import "./command.css";
import type { ReactNode } from "react";
import { ToastProvider } from "../components/ToastProvider";

export const metadata = { title: "Whirlwind · Engineering in motion", description: "Ideas in. Verified software out. An agent-agnostic engineering harness for autonomous, evidence-driven execution loops." };

export default function RootLayout({ children }: { children: ReactNode }) {
  return <html lang="en"><body><ToastProvider>{children}</ToastProvider></body></html>;
}
