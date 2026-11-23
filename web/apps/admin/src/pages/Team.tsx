import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { MailIcon, MoreHorizontalIcon, ShieldCheckIcon, ShieldOffIcon, Trash2Icon, UserPlusIcon, UsersIcon } from "lucide-react";
import { formatDateTime, timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Badge, Card, EmptyState, Skeleton, Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@drix/ui/components/display";
import { Field, Input, Switch } from "@drix/ui/components/form";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
} from "@drix/ui/components/menu";
import { ConfirmDialog, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@drix/ui/components/overlay";
import { api, ApiError, type AuditEntry, type Role, type TeamResponse, type User } from "../api";
import { useMe } from "../lib/queries";
import { atLeast, roleDescription, roleLabel, useCan } from "../lib/roles";
import { CopyButton, PageHeader } from "../components/Page";

const roles: Role[] = ["owner", "admin", "editor", "viewer"];

export default function TeamPage() {
  const can = useCan();
  const [inviteOpen, setInviteOpen] = useState(false);
  return (
    <>
      <PageHeader
        title="Team"
        description="People who can sign in to this admin panel, and what they're allowed to do."
        actions={
          can("admin") && (
            <Button onClick={() => setInviteOpen(true)}>
              <UserPlusIcon /> Invite
            </Button>
          )
        }
      />
      {can("admin") ? (
        <Tabs defaultValue="members" className="grid gap-4">
          <TabsList className="w-fit">
            <TabsTrigger value="members">Members</TabsTrigger>
            <TabsTrigger value="audit">Audit log</TabsTrigger>
          </TabsList>
          <TabsContent value="members">
            <Members />
          </TabsContent>
          <TabsContent value="audit">
            <AuditLog />
          </TabsContent>
        </Tabs>
      ) : (
        <Members />
      )}
      <InviteDialog open={inviteOpen} onOpenChange={setInviteOpen} />
    </>
  );
}

function Members() {
  const qc = useQueryClient();
  const { data: me } = useMe();
  const { data, isLoading } = useQuery({ queryKey: ["team"], queryFn: () => api.get<TeamResponse>("/users") });
  const [removing, setRemoving] = useState<User | null>(null);
  const [resetting, setResetting] = useState<User | null>(null);
  const actor = me?.user;
  const isOwner = actor?.role === "owner";
  const { data: security } = useQuery({ queryKey: ["security"], queryFn: () => api.get<{ require_two_factor: boolean }>("/security") });
  const setRequire = useMutation({
    mutationFn: (v: boolean) => api.put<{ require_two_factor: boolean }>("/security", { require_two_factor: v }),
    onSuccess: (r) => {
      qc.setQueryData(["security"], r);
      toast.success(r.require_two_factor ? "Two-factor sign-in is now required" : "Two-factor sign-in is now optional");
    },
    onError: (e) => toast.error(e.message),
  });
  const reset = useMutation({
    mutationFn: (u: User) => api.del(`/users/${u.id}/two-factor`),
    onSuccess: (_, u) => {
      toast.success(`Two-factor reset for ${u.email}. They're signed out and can set it up again.`);
      qc.invalidateQueries({ queryKey: ["team"] });
    },
    onError: (e) => toast.error(e.message),
  });

  const update = useMutation({
    mutationFn: (u: { id: number; name: string; role: Role }) => api.put<User>(`/users/${u.id}`, { name: u.name, role: u.role }),
    onSuccess: (u) => {
      toast.success(`${u.name || u.email} is now ${roleLabel[u.role].toLowerCase()}`);
      qc.invalidateQueries({ queryKey: ["team"] });
      qc.invalidateQueries({ queryKey: ["me"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const remove = useMutation({
    mutationFn: (u: User) => api.del(`/users/${u.id}`),
    onSuccess: (_, u) => {
      toast.success(`${u.email} was removed`);
      qc.invalidateQueries({ queryKey: ["team"] });
    },
    onError: (e) => toast.error(e.message),
  });
  const revoke = useMutation({
    mutationFn: (id: number) => api.del(`/invites/${id}`),
    onSuccess: () => {
      toast.success("Invite revoked");
      qc.invalidateQueries({ queryKey: ["team"] });
    },
  });

  // Mirrors the server's rule: admins manage non-owners; only owners touch owners.
  const canManage = (target: User) =>
    !!actor && atLeast(actor.role, "admin") && (target.role !== "owner" || actor.role === "owner");
  const assignable = roles.filter((r) => r !== "owner" || actor?.role === "owner");

  if (isLoading || !data) return <Skeleton className="h-64 rounded-xl" />;

  return (
    <div className="grid gap-6">
      {(isOwner || security?.require_two_factor) && (
        <Card className="flex items-center gap-3 px-4 py-3">
          <ShieldCheckIcon className="size-4 shrink-0 text-muted-foreground" />
          <div className="min-w-0 flex-1">
            <p className="text-[13px] font-medium">Require two-factor sign-in</p>
            <p className="text-xs text-muted-foreground">
              Everyone must add a passkey or an authenticator app before they can use the admin. API keys aren't affected.
            </p>
          </div>
          {isOwner ? (
            <Switch
              checked={!!security?.require_two_factor}
              disabled={setRequire.isPending}
              onCheckedChange={(v) => setRequire.mutate(v)}
              aria-label="Require two-factor sign-in for everyone"
            />
          ) : (
            <Badge variant="secondary">Required</Badge>
          )}
        </Card>
      )}
      <Card className="overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="pl-4">Member</TableHead>
              <TableHead>Role</TableHead>
              <TableHead className="hidden sm:table-cell">Two-factor</TableHead>
              <TableHead className="hidden md:table-cell">Last sign-in</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {data.users.map((u) => (
              <TableRow key={u.id}>
                <TableCell className="pl-4">
                  <div className="flex items-center gap-3">
                    <span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-muted text-xs font-semibold uppercase">
                      {(u.name || u.email).slice(0, 1)}
                    </span>
                    <div className="min-w-0">
                      <p className="truncate font-medium">
                        {u.name || u.email.split("@")[0]}
                        {u.id === actor?.id && <span className="ml-1.5 text-xs font-normal text-muted-foreground">(you)</span>}
                      </p>
                      <p className="truncate text-xs text-muted-foreground">{u.email}</p>
                    </div>
                  </div>
                </TableCell>
                <TableCell>
                  {canManage(u) ? (
                    <Select value={u.role} onValueChange={(r) => update.mutate({ id: u.id, name: u.name, role: r as Role })}>
                      <SelectTrigger className="h-7 w-28 text-xs">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {assignable.map((r) => (
                          <SelectItem key={r} value={r}>
                            {roleLabel[r]}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  ) : (
                    <Badge variant={u.role === "owner" ? "default" : "secondary"}>{roleLabel[u.role]}</Badge>
                  )}
                </TableCell>
                <TableCell className="hidden text-xs sm:table-cell">
                  {u.two_factor ? (
                    <span className="inline-flex items-center gap-1.5 text-foreground">
                      <ShieldCheckIcon className="size-3.5 text-up" /> On
                    </span>
                  ) : (
                    <span className="inline-flex items-center gap-1.5 text-muted-foreground">
                      <ShieldOffIcon className="size-3.5" /> Off
                    </span>
                  )}
                </TableCell>
                <TableCell className="hidden text-xs text-muted-foreground md:table-cell">
                  {u.last_login_at ? timeAgo(u.last_login_at) : "Never"}
                </TableCell>
                <TableCell>
                  {canManage(u) && u.id !== actor?.id && (
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={`Actions for ${u.email}`}>
                          <MoreHorizontalIcon />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        {isOwner && u.two_factor && (
                          <DropdownMenuItem onSelect={() => setResetting(u)}>
                            <ShieldOffIcon /> Reset two-factor
                          </DropdownMenuItem>
                        )}
                        <DropdownMenuItem variant="destructive" onSelect={() => setRemoving(u)}>
                          <Trash2Icon /> Remove from team
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </Card>

      {data.invites.length > 0 && (
        <div>
          <h2 className="mb-2 text-[13px] font-semibold">Pending invites</h2>
          <Card className="divide-y">
            {data.invites.map((inv) => {
              const expired = new Date(inv.expires_at).getTime() < Date.now();
              return (
                <div key={inv.id} className="flex items-center gap-3 px-4 py-3">
                  <MailIcon className="size-4 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <p className="truncate text-[13px] font-medium">{inv.email}</p>
                    <p className="text-xs text-muted-foreground">
                      {roleLabel[inv.role]} · {expired ? "expired" : `expires ${timeAgo(inv.expires_at)}`}
                    </p>
                  </div>
                  {atLeast(actor?.role, "admin") && (
                    <Button variant="ghost" size="sm" onClick={() => revoke.mutate(inv.id)}>
                      Revoke
                    </Button>
                  )}
                </div>
              );
            })}
          </Card>
        </div>
      )}

      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-4">
        {roles.map((r) => (
          <div key={r} className="rounded-lg border px-3 py-2.5">
            <p className="text-[13px] font-medium">{roleLabel[r]}</p>
            <p className="mt-0.5 text-xs text-muted-foreground">{roleDescription[r]}</p>
          </div>
        ))}
      </div>

      <ConfirmDialog
        open={!!resetting}
        onOpenChange={(o) => !o && setResetting(null)}
        title={`Reset two-factor for ${resetting?.email}?`}
        description="Their passkeys, authenticator app and recovery codes are removed and they're signed out. Use this when someone lost their phone and codes."
        confirmLabel="Reset two-factor"
        destructive
        onConfirm={() => resetting && reset.mutate(resetting)}
      />
      <ConfirmDialog
        open={!!removing}
        onOpenChange={(o) => !o && setRemoving(null)}
        title={`Remove ${removing?.email}?`}
        description="They're signed out right away and can no longer access the admin panel."
        confirmLabel="Remove"
        destructive
        onConfirm={() => removing && remove.mutate(removing)}
      />
    </div>
  );
}

function InviteDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (o: boolean) => void }) {
  const qc = useQueryClient();
  const { data: me } = useMe();
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("editor");
  const [error, setError] = useState<string | null>(null);
  const [result, setResult] = useState<{ link: string; emailed: boolean; email: string } | null>(null);

  const close = (o: boolean) => {
    onOpenChange(o);
    if (!o) {
      setEmail("");
      setRole("editor");
      setError(null);
      setResult(null);
    }
  };

  const send = useMutation({
    mutationFn: () => api.post<{ link: string; emailed: boolean }>("/invites", { email, role }),
    onSuccess: (r) => {
      setResult({ ...r, email });
      qc.invalidateQueries({ queryKey: ["team"] });
    },
    onError: (e) => setError(e instanceof ApiError ? e.message : "Could not create invite"),
  });

  const assignable = roles.filter((r) => r !== "owner" || me?.user.role === "owner");

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        {result ? (
          <div className="grid gap-4">
            <DialogHeader>
              <DialogTitle>Invite ready</DialogTitle>
              <DialogDescription>
                {result.emailed
                  ? `We emailed ${result.email} a link. You can also share it yourself:`
                  : `Email isn't set up yet, so send ${result.email} this link yourself. It works for 7 days.`}
              </DialogDescription>
            </DialogHeader>
            <div className="flex items-center gap-2 rounded-lg border bg-muted/50 py-1 pr-1 pl-3">
              <code className="flex-1 truncate font-mono text-[12px]">{result.link}</code>
              <CopyButton value={result.link} label="Copy" />
            </div>
            <DialogFooter>
              <Button onClick={() => close(false)}>Done</Button>
            </DialogFooter>
          </div>
        ) : (
          <form
            className="grid gap-4"
            onSubmit={(e) => {
              e.preventDefault();
              setError(null);
              send.mutate();
            }}
          >
            <DialogHeader>
              <DialogTitle>Invite a team member</DialogTitle>
              <DialogDescription>They'll choose their own name and password when they accept.</DialogDescription>
            </DialogHeader>
            <Field label="Email" htmlFor="inv-email" error={error ?? undefined}>
              <Input id="inv-email" type="email" autoFocus required value={email} onChange={(e) => setEmail(e.target.value)} placeholder="name@company.com" />
            </Field>
            <div className="grid gap-1.5">
              <span className="text-[13px] font-medium">Role</span>
              <div className="grid gap-1.5">
                {assignable.map((r) => (
                  <label
                    key={r}
                    className="flex cursor-pointer items-start gap-2.5 rounded-lg border px-3 py-2 has-[:checked]:border-foreground/50 has-[:checked]:bg-muted/50"
                  >
                    <input type="radio" name="role" value={r} checked={role === r} onChange={() => setRole(r)} className="mt-0.5 accent-current" />
                    <span>
                      <span className="block text-[13px] font-medium">{roleLabel[r]}</span>
                      <span className="block text-xs text-muted-foreground">{roleDescription[r]}</span>
                    </span>
                  </label>
                ))}
              </div>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => close(false)}>
                Cancel
              </Button>
              <Button type="submit" disabled={send.isPending || !email}>
                Create invite
              </Button>
            </DialogFooter>
          </form>
        )}
      </DialogContent>
    </Dialog>
  );
}

function AuditLog() {
  const { data, isLoading } = useQuery({ queryKey: ["audit"], queryFn: () => api.get<AuditEntry[]>("/audit?limit=200"), refetchInterval: 30_000 });
  if (isLoading) return <Skeleton className="h-64 rounded-xl" />;
  if (!data?.length) {
    return (
      <Card>
        <EmptyState icon={<UsersIcon />} title="Nothing recorded yet" description="Changes made in the admin panel show up here." />
      </Card>
    );
  }
  return (
    <Card className="overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="pl-4">When</TableHead>
            <TableHead>Who</TableHead>
            <TableHead>What</TableHead>
            <TableHead className="hidden lg:table-cell">IP</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {data.map((e) => (
            <TableRow key={e.id}>
              <TableCell className="tabular pl-4 text-xs text-muted-foreground" title={formatDateTime(e.created_at)}>
                {timeAgo(e.created_at)}
              </TableCell>
              <TableCell className="text-xs">{e.user_email || "system"}</TableCell>
              <TableCell className="max-w-md">
                <span className="mr-2 rounded bg-muted px-1.5 py-px font-mono text-[11px] text-muted-foreground">{e.action}</span>
                <span className="text-[13px]">{e.summary}</span>
              </TableCell>
              <TableCell className="hidden font-mono text-[11px] text-muted-foreground lg:table-cell">{e.ip}</TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}
