import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import { KeyRoundIcon, Loader2Icon, PlusIcon, Trash2Icon } from "lucide-react";
import { timeAgo } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Badge } from "@drix/ui/components/display";
import { Field, Input } from "@drix/ui/components/form";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@drix/ui/components/menu";
import { ConfirmDialog, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@drix/ui/components/overlay";
import { api, ApiError, type ApiKey, type Role } from "../api";
import { useMe } from "../lib/queries";
import { CodeBlock, CopyButton } from "./Page";

const roles: { value: Exclude<Role, "owner">; label: string; hint: string }[] = [
  { value: "viewer", label: "Viewer", hint: "Read monitors, incidents and results" },
  { value: "editor", label: "Editor", hint: "Also pause monitors and post incidents" },
  { value: "admin", label: "Admin", hint: "Also change settings, workers and alerts" },
];
const rank: Record<Role, number> = { viewer: 1, editor: 2, admin: 3, owner: 4 };

/** Settings → API keys: keys for scripts and CI. */
export function ApiKeysSection({ Section }: { Section: React.ComponentType<{ title: string; description: string; children: React.ReactNode }> }) {
  const qc = useQueryClient();
  const { data: keys } = useQuery({ queryKey: ["api-keys"], queryFn: () => api.get<ApiKey[]>("/api-keys") });
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<ApiKey | null>(null);
  const [deleting, setDeleting] = useState<ApiKey | null>(null);

  const remove = useMutation({
    mutationFn: (k: ApiKey) => api.del(`/api-keys/${k.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["api-keys"] });
      toast.success("API key deleted");
    },
    onError: (e) => toast.error(e.message),
  });

  return (
    <Section title="API keys" description="Let scripts and CI use the admin API, for example to pause monitors during a deploy. A key acts as you, with the role you pick.">
      <div className="flex items-center">
        <Button type="button" variant="outline" size="sm" className="ml-auto" onClick={() => setCreating(true)}>
          <PlusIcon />
          New key
        </Button>
      </div>
      {!keys?.length ? (
        <p className="rounded-lg border border-dashed px-4 py-6 text-center text-[13px] text-muted-foreground">No API keys yet.</p>
      ) : (
        <ul className="divide-y rounded-lg border">
          {keys.map((k) => (
            <li key={k.id} className="flex items-center gap-3 px-3 py-2.5 text-[13px]">
              <KeyRoundIcon className="size-3.5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium">{k.name}</p>
                <p className="truncate text-xs text-muted-foreground">
                  <code className="font-mono">{k.prefix}…</code> · by {k.created_by_email} · {k.last_used_at ? `used ${timeAgo(k.last_used_at)}` : "never used"}
                </p>
              </div>
              <Badge variant="muted" className="capitalize">{k.role}</Badge>
              <Button type="button" variant="ghost" size="icon-sm" aria-label={`Delete ${k.name}`} onClick={() => setDeleting(k)} className="hover:text-destructive">
                <Trash2Icon />
              </Button>
            </li>
          ))}
        </ul>
      )}
      {creating && (
        <CreateKeyDialog
          onClose={() => setCreating(false)}
          onCreated={(k) => {
            setCreating(false);
            setCreated(k);
            qc.invalidateQueries({ queryKey: ["api-keys"] });
          }}
        />
      )}
      {created && <KeyCreatedDialog k={created} onClose={() => setCreated(null)} />}
      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(o) => !o && setDeleting(null)}
        title={`Delete ${deleting?.name}?`}
        description="Anything using this key stops working right away. This can't be undone."
        confirmLabel="Delete key"
        destructive
        onConfirm={() => deleting && remove.mutate(deleting)}
      />
    </Section>
  );
}

function CreateKeyDialog({ onClose, onCreated }: { onClose: () => void; onCreated: (k: ApiKey) => void }) {
  const { data: me } = useMe();
  const myRank = rank[me?.user.role ?? "viewer"];
  const [name, setName] = useState("");
  const [role, setRole] = useState<Exclude<Role, "owner">>("editor");
  const [errors, setErrors] = useState<Record<string, string>>({});
  const create = useMutation({
    mutationFn: () => api.post<ApiKey>("/api-keys", { name, role }),
    onSuccess: onCreated,
    onError: (e) => {
      if (e instanceof ApiError && e.fields) setErrors(e.fields);
      else toast.error(e.message);
    },
  });
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <DialogHeader>
            <DialogTitle>New API key</DialogTitle>
            <DialogDescription>Give it a name you'll recognise later, like "GitHub Actions".</DialogDescription>
          </DialogHeader>
          <Field label="Name" htmlFor="key-name" error={errors.name}>
            <Input id="key-name" autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="GitHub Actions" maxLength={80} />
          </Field>
          <Field label="Role" error={errors.role} hint={roles.find((r) => r.value === role)?.hint}>
            <Select value={role} onValueChange={(v) => setRole(v as typeof role)}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {roles.map((r) => (
                  <SelectItem key={r.value} value={r.value} disabled={rank[r.value] > myRank}>
                    {r.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </Field>
          <DialogFooter>
            <Button type="button" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2Icon className="animate-spin" />}
              Create key
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function KeyCreatedDialog({ k, onClose }: { k: ApiKey; onClose: () => void }) {
  const { data: me } = useMe();
  const base = (me?.public_url || location.origin).replace(/\/$/, "");
  const example = `export DRIX_API_KEY=${k.token}

# List monitors
curl -H "Authorization: Bearer $DRIX_API_KEY" \\
  ${base}/api/admin/monitors

# Pause monitors 1 and 2 during a deploy ("resume" works the same way)
curl -X POST -H "Authorization: Bearer $DRIX_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{"action":"pause","ids":[1,2]}' \\
  ${base}/api/admin/monitors/bulk`;
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="grid-cols-[minmax(0,1fr)] sm:max-w-xl">
        <DialogHeader>
          <DialogTitle>{k.name}</DialogTitle>
          <DialogDescription>Copy the key now. It won't be shown again.</DialogDescription>
        </DialogHeader>
        <div className="flex min-w-0 items-center gap-2 rounded-lg border bg-muted/50 py-1 pr-1 pl-3">
          <code className="min-w-0 flex-1 truncate font-mono text-[12.5px]">{k.token}</code>
          <CopyButton value={k.token ?? ""} label="Copy" />
        </div>
        <CodeBlock code={example} />
        <p className="text-xs text-muted-foreground">Send the key in the Authorization header. Keys can't manage the team, passwords or other keys.</p>
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
