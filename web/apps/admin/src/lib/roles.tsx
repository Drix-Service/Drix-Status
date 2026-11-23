import type { ReactNode } from "react";
import type { Role } from "../api";
import { useMe } from "./queries";

const rank: Record<Role, number> = { viewer: 1, editor: 2, admin: 3, owner: 4 };

export const roleLabel: Record<Role, string> = {
  owner: "Owner",
  admin: "Admin",
  editor: "Editor",
  viewer: "Viewer",
};

export const roleDescription: Record<Role, string> = {
  owner: "Everything, including managing other owners.",
  admin: "Everything except changing owners: team, workers, alerts and settings.",
  editor: "Monitors, status pages, incidents and maintenance.",
  viewer: "Read-only access to everything.",
};

export const atLeast = (role: Role | undefined, min: Role) => !!role && rank[role] >= rank[min];

/** Returns a checker for the signed-in user's role. The server enforces the same rules. */
export function useCan() {
  const { data } = useMe();
  return (min: Role) => atLeast(data?.user.role, min);
}

/** Renders children only when the signed-in user has at least `role`. */
export function Can({ role, children, fallback = null }: { role: Role; children: ReactNode; fallback?: ReactNode }) {
  const can = useCan();
  return <>{can(role) ? children : fallback}</>;
}
