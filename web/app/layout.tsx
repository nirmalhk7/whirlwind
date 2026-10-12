import "./globals.css";
import type { ReactNode } from "react";
import { ToastProvider } from "../components/ToastProvider";

export const metadata = { title: "Khan · Local control center", description: "Evidence and execution status for local Khan runs." };

export default function RootLayout({ children }: { children: ReactNode }) {
  return <html lang="en"><body><ToastProvider>{children}</ToastProvider></body></html>;
}
