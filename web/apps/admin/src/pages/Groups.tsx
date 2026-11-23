import { useEffect, useMemo, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import {
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  closestCorners,
  useDroppable,
  useSensor,
  useSensors,
  type DragEndEvent,
  type DragOverEvent,
  type DragStartEvent,
} from "@dnd-kit/core";
import { SortableContext, arrayMove, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  ArrowDownIcon,
  ArrowUpIcon,
  ArrowUpRightIcon,
  CheckIcon,
  GripVerticalIcon,
  Loader2Icon,
  PencilIcon,
  PlusIcon,
  SearchIcon,
  Trash2Icon,
  XIcon,
} from "lucide-react";
import { cn } from "@drix/ui/lib/utils";
import { Button } from "@drix/ui/components/button";
import { Card } from "@drix/ui/components/display";
import { Input, Switch } from "@drix/ui/components/form";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@drix/ui/components/menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@drix/ui/components/overlay";
import { MonitorTypeIcon, StatusDot } from "@drix/ui/drix/status";
import { api, ApiError, type Monitor, type Section } from "../api";
import { keys, useGroups, useMonitors } from "../lib/queries";
import { statusPageUrl } from "../components/Shell";
import { useCan } from "../lib/roles";
import { PageHeader, PageSkeleton } from "../components/Page";

export default function GroupsPage() {
  const { data, isLoading } = useGroups();
  return (
    <>
      <PageHeader
        title="Groups"
        description="Sections on your status page, like “Website & API” or “EU servers”. Drag monitors between groups; a monitor can be in more than one."
        actions={
          <Button asChild variant="outline">
            <a href={statusPageUrl} target="_blank" rel="noreferrer">
              View status page <ArrowUpRightIcon />
            </a>
          </Button>
        }
      />
      {isLoading || !data ? <PageSkeleton /> : <LayoutEditor groups={data.groups} />}
    </>
  );
}

/* ---------------- Layout editor ---------------- */

type Item = { key: string; monitor_id: number; display_name: string | null };
type SectionDraft = { key: string; id?: number; name: string; description: string; collapsed: boolean; items: Item[] };

let keySeq = 0;
const nextKey = (p: string) => `${p}-${++keySeq}`;

function toDraft(groups: Section[]): SectionDraft[] {
  return groups.map((s) => ({
    key: nextKey("s"),
    id: s.id,
    name: s.name,
    description: s.description,
    collapsed: s.collapsed,
    items: s.monitors.map((m) => ({ key: nextKey("m"), monitor_id: m.monitor_id, display_name: m.display_name })),
  }));
}

function serialize(sections: SectionDraft[]) {
  return {
    groups: sections.map((s) => ({
      id: s.id ?? 0,
      name: s.name.trim(),
      description: s.description.trim(),
      collapsed: s.collapsed,
      monitors: s.items.map((i) => ({ monitor_id: i.monitor_id, display_name: i.display_name?.trim() || null })),
    })),
  };
}

function LayoutEditor({ groups }: { groups: Section[] }) {
  const qc = useQueryClient();
  const canEdit = useCan()("editor");
  const { data: monitors } = useMonitors();
  const [sections, setSections] = useState<SectionDraft[]>(() => toDraft(groups));
  const [baseline, setBaseline] = useState(() => JSON.stringify(serialize(toDraft(groups))));
  const [activeKey, setActiveKey] = useState<string | null>(null);
  const [target, setTarget] = useState<string>("");

  // Re-sync when the server copy changes and we have no local edits.
  const dirty = JSON.stringify(serialize(sections)) !== baseline;
  useEffect(() => {
    if (!dirty) {
      setSections(toDraft(groups));
      setBaseline(JSON.stringify(serialize(toDraft(groups))));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [groups]);

  const byId = useMemo(() => new Map((monitors ?? []).map((m) => [m.id, m])), [monitors]);
  const onPage = useMemo(() => new Set(sections.flatMap((s) => s.items.map((i) => i.monitor_id))), [sections]);
  const targetKey = sections.some((s) => s.key === target) ? target : sections[sections.length - 1]?.key;

  const save = useMutation({
    mutationFn: () => api.put<{ groups: Section[] }>("/groups", serialize(sections)),
    onSuccess: (r) => {
      const d = r.groups;
      qc.setQueryData(keys.groups, r);
      qc.invalidateQueries({ queryKey: keys.monitors });
      setSections(toDraft(d));
      setBaseline(JSON.stringify(serialize(toDraft(d))));
      toast.success("Groups saved", { description: "The status page updates within a second." });
    },
    onError: (e) => toast.error(e instanceof ApiError ? e.message : "Could not save groups"),
  });

  const update = (key: string, patch: Partial<SectionDraft>) => setSections((ss) => ss.map((s) => (s.key === key ? { ...s, ...patch } : s)));
  const addSection = () => {
    const key = nextKey("s");
    setSections((ss) => [...ss, { key, name: "New group", description: "", collapsed: false, items: [] }]);
    setTarget(key);
  };
  const moveSection = (i: number, dir: -1 | 1) => setSections((ss) => arrayMove(ss, i, i + dir));
  const addMonitor = (m: Monitor) => {
    if (!targetKey) return;
    setSections((ss) => ss.map((s) => (s.key === targetKey ? { ...s, items: [...s.items, { key: nextKey("m"), monitor_id: m.id, display_name: null }] } : s)));
  };
  const removeItem = (sKey: string, iKey: string) =>
    setSections((ss) => ss.map((s) => (s.key === sKey ? { ...s, items: s.items.filter((i) => i.key !== iKey) } : s)));
  const renameItem = (sKey: string, iKey: string, name: string | null) =>
    setSections((ss) => ss.map((s) => (s.key === sKey ? { ...s, items: s.items.map((i) => (i.key === iKey ? { ...i, display_name: name } : i)) } : s)));

  // ---- drag & drop across sections ----
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }));
  const sectionOf = (key: string) => sections.find((s) => s.key === key || s.items.some((i) => i.key === key));

  const onDragStart = (e: DragStartEvent) => setActiveKey(String(e.active.id));
  const onDragOver = (e: DragOverEvent) => {
    const { active, over } = e;
    if (!over) return;
    const from = sectionOf(String(active.id));
    const to = sectionOf(String(over.id));
    if (!from || !to || from.key === to.key) return;
    setSections((ss) => {
      const item = from.items.find((i) => i.key === active.id)!;
      const overIndex = to.items.findIndex((i) => i.key === over.id);
      return ss.map((s) => {
        if (s.key === from.key) return { ...s, items: s.items.filter((i) => i.key !== active.id) };
        if (s.key === to.key) {
          const items = [...s.items];
          items.splice(overIndex < 0 ? items.length : overIndex, 0, item);
          return { ...s, items };
        }
        return s;
      });
    });
  };
  const onDragEnd = (e: DragEndEvent) => {
    setActiveKey(null);
    const { active, over } = e;
    if (!over || active.id === over.id) return;
    const sec = sectionOf(String(active.id));
    if (!sec) return;
    const a = sec.items.findIndex((i) => i.key === active.id);
    const b = sec.items.findIndex((i) => i.key === over.id);
    if (a >= 0 && b >= 0) update(sec.key, { items: arrayMove(sec.items, a, b) });
  };
  const activeItem = activeKey ? sections.flatMap((s) => s.items).find((i) => i.key === activeKey) : null;

  return (
    <div className="grid gap-6 lg:grid-cols-[1fr_20rem]">
      <div className="grid min-w-0 content-start gap-3">
        <DndContext sensors={sensors} collisionDetection={closestCorners} onDragStart={onDragStart} onDragOver={onDragOver} onDragEnd={onDragEnd}>
          {sections.map((s, i) => (
            <SectionCard
              key={s.key}
              section={s}
              index={i}
              count={sections.length}
              byId={byId}
              canEdit={canEdit}
              isTarget={s.key === targetKey}
              onTarget={() => setTarget(s.key)}
              onChange={(patch) => update(s.key, patch)}
              onMove={(dir) => moveSection(i, dir)}
              onDelete={() => setSections((ss) => ss.filter((x) => x.key !== s.key))}
              onRemoveItem={(iKey) => removeItem(s.key, iKey)}
              onRenameItem={(iKey, n) => renameItem(s.key, iKey, n)}
            />
          ))}
          <DragOverlay>{activeItem ? <ItemRow item={activeItem} monitor={byId.get(activeItem.monitor_id)} overlay /> : null}</DragOverlay>
        </DndContext>
        {canEdit && (
          <Button variant="outline" className="w-full border-dashed" onClick={addSection}>
            <PlusIcon /> Add group
          </Button>
        )}
        {canEdit && (
          <div
            className={cn(
              "sticky bottom-4 z-10 flex items-center gap-3 rounded-xl border bg-popover/95 px-4 py-2.5 shadow-lg backdrop-blur transition-all",
              dirty ? "translate-y-0 opacity-100" : "pointer-events-none translate-y-2 opacity-0",
            )}
          >
            <span className="text-[13px] font-medium">Unsaved changes</span>
            <div className="ml-auto flex gap-2">
              <Button
                variant="ghost"
                onClick={() => {
                  setSections(toDraft(groups));
                }}
              >
                Discard
              </Button>
              <Button onClick={() => save.mutate()} disabled={save.isPending}>
                {save.isPending && <Loader2Icon className="animate-spin" />}
                Save groups
              </Button>
            </div>
          </div>
        )}
      </div>

      {canEdit && (
        <MonitorPicker
          monitors={monitors ?? []}
          onPage={onPage}
          sections={sections}
          targetKey={targetKey}
          onTarget={setTarget}
          onAdd={addMonitor}
        />
      )}
    </div>
  );
}

function SectionCard({
  section: s,
  index,
  count,
  byId,
  canEdit,
  isTarget,
  onTarget,
  onChange,
  onMove,
  onDelete,
  onRemoveItem,
  onRenameItem,
}: {
  section: SectionDraft;
  index: number;
  count: number;
  byId: Map<number, Monitor>;
  canEdit: boolean;
  isTarget: boolean;
  onTarget: () => void;
  onChange: (p: Partial<SectionDraft>) => void;
  onMove: (dir: -1 | 1) => void;
  onDelete: () => void;
  onRemoveItem: (key: string) => void;
  onRenameItem: (key: string, name: string | null) => void;
}) {
  const { setNodeRef, isOver } = useDroppable({ id: s.key });
  return (
    <Card className={cn("overflow-hidden transition-shadow", isTarget && canEdit && "ring-1 ring-brand/40")} onClick={onTarget}>
      <div className="flex items-start gap-2 border-b bg-muted/30 px-3 py-2.5">
        <div className="grid min-w-0 flex-1 gap-1">
          <input
            value={s.name}
            disabled={!canEdit}
            onChange={(e) => onChange({ name: e.target.value })}
            className="w-full rounded bg-transparent px-1 text-[14px] font-semibold tracking-tight outline-none focus:bg-background focus:ring-1 focus:ring-ring/40"
            aria-label="Group name"
          />
          <input
            value={s.description}
            disabled={!canEdit}
            placeholder={canEdit ? "Add a description (optional)" : ""}
            onChange={(e) => onChange({ description: e.target.value })}
            className="w-full rounded bg-transparent px-1 text-xs text-muted-foreground outline-none placeholder:text-muted-foreground/60 focus:bg-background focus:ring-1 focus:ring-ring/40"
            aria-label="Group description"
          />
        </div>
        {canEdit && (
          <div className="flex shrink-0 items-center gap-0.5">
            <Tooltip>
              <TooltipTrigger asChild>
                <label className="mr-1 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                  Collapsed
                  <Switch checked={s.collapsed} onCheckedChange={(v) => onChange({ collapsed: v })} />
                </label>
              </TooltipTrigger>
              <TooltipContent>Visitors see the group closed until they open it</TooltipContent>
            </Tooltip>
            <Button variant="ghost" size="icon-sm" disabled={index === 0} onClick={() => onMove(-1)} aria-label="Move group up">
              <ArrowUpIcon />
            </Button>
            <Button variant="ghost" size="icon-sm" disabled={index === count - 1} onClick={() => onMove(1)} aria-label="Move group down">
              <ArrowDownIcon />
            </Button>
            <Button variant="ghost" size="icon-sm" onClick={onDelete} aria-label="Delete group" className="hover:text-destructive">
              <Trash2Icon />
            </Button>
          </div>
        )}
      </div>
      <SortableContext items={s.items.map((i) => i.key)} strategy={verticalListSortingStrategy}>
        <ul ref={setNodeRef} className={cn("min-h-12 divide-y transition-colors", isOver && "bg-brand/[0.04]")}>
          {s.items.map((i) => (
            <SortableItem key={i.key} item={i} monitor={byId.get(i.monitor_id)} canEdit={canEdit} onRemove={() => onRemoveItem(i.key)} onRename={(n) => onRenameItem(i.key, n)} />
          ))}
          {s.items.length === 0 && (
            <li className="px-4 py-4 text-center text-xs text-muted-foreground">
              {canEdit ? "Drag monitors here, or add them from the list" : "No monitors in this group"}
            </li>
          )}
        </ul>
      </SortableContext>
    </Card>
  );
}

function SortableItem({ item, monitor, canEdit, onRemove, onRename }: { item: Item; monitor?: Monitor; canEdit: boolean; onRemove: () => void; onRename: (n: string | null) => void }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: item.key, disabled: !canEdit });
  return (
    <li ref={setNodeRef} style={{ transform: CSS.Translate.toString(transform), transition }} className={cn(isDragging && "opacity-40")}>
      <ItemRow item={item} monitor={monitor} canEdit={canEdit} handle={{ ...attributes, ...listeners }} onRemove={onRemove} onRename={onRename} />
    </li>
  );
}

function ItemRow({
  item,
  monitor,
  canEdit,
  handle,
  overlay,
  onRemove,
  onRename,
}: {
  item: Item;
  monitor?: Monitor;
  canEdit?: boolean;
  handle?: Record<string, unknown>;
  overlay?: boolean;
  onRemove?: () => void;
  onRename?: (n: string | null) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(item.display_name ?? "");
  return (
    <div className={cn("flex items-center gap-2 bg-card px-2 py-2", overlay && "rounded-lg border shadow-xl")}>
      {canEdit !== false && (
        <button {...handle} className="cursor-grab touch-none rounded p-1 text-muted-foreground hover:bg-muted active:cursor-grabbing" aria-label="Drag to move">
          <GripVerticalIcon className="size-4" />
        </button>
      )}
      <StatusDot status={monitor?.live.status ?? "pending"} size="sm" />
      {monitor && <MonitorTypeIcon type={monitor.type} className="size-3.5" />}
      {editing ? (
        <form
          className="flex flex-1 items-center gap-1"
          onSubmit={(e) => {
            e.preventDefault();
            onRename?.(draft.trim() || null);
            setEditing(false);
          }}
        >
          <Input autoFocus value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={monitor?.name} className="h-7 text-xs" />
          <Button type="submit" size="icon-sm" variant="ghost" aria-label="Save name">
            <CheckIcon />
          </Button>
        </form>
      ) : (
        <div className="min-w-0 flex-1">
          <p className="truncate text-[13px] font-medium">{item.display_name || monitor?.name || `Monitor #${item.monitor_id}`}</p>
          {item.display_name && <p className="truncate text-[11px] text-muted-foreground">Monitor: {monitor?.name}</p>}
        </div>
      )}
      {canEdit && !editing && !overlay && (
        <>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-sm" onClick={() => setEditing(true)} aria-label="Rename on the status page">
                <PencilIcon />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Show a different name on the status page</TooltipContent>
          </Tooltip>
          <Button variant="ghost" size="icon-sm" onClick={onRemove} aria-label="Remove from group" className="hover:text-destructive">
            <XIcon />
          </Button>
        </>
      )}
    </div>
  );
}

function MonitorPicker({
  monitors,
  onPage,
  sections,
  targetKey,
  onTarget,
  onAdd,
}: {
  monitors: Monitor[];
  onPage: Set<number>;
  sections: SectionDraft[];
  targetKey?: string;
  onTarget: (k: string) => void;
  onAdd: (m: Monitor) => void;
}) {
  const [q, setQ] = useState("");
  const list = monitors
    .filter((m) => !q || m.name.toLowerCase().includes(q.toLowerCase()) || m.target.toLowerCase().includes(q.toLowerCase()))
    .sort((a, b) => Number(onPage.has(a.id)) - Number(onPage.has(b.id)) || a.name.localeCompare(b.name)); // ungrouped first
  return (
    <Card className="flex max-h-[calc(100dvh-10rem)] flex-col self-start lg:sticky lg:top-20">
      <div className="grid gap-2 border-b p-3">
        <p className="text-[13px] font-semibold">Add monitors</p>
        {sections.length > 0 ? (
          <Select value={targetKey} onValueChange={onTarget}>
            <SelectTrigger className="h-7 text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {sections.map((s) => (
                <SelectItem key={s.key} value={s.key}>
                  Into “{s.name || "Untitled"}”
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        ) : (
          <p className="text-xs text-muted-foreground">Add a group first.</p>
        )}
        <div className="relative">
          <SearchIcon className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search" className="h-7 pl-7 text-xs" />
        </div>
      </div>
      <ul className="flex-1 divide-y overflow-y-auto">
        {list.map((m) => {
          const inTarget = !!sections.find((s) => s.key === targetKey)?.items.some((i) => i.monitor_id === m.id);
          const groupCount = sections.filter((s) => s.items.some((i) => i.monitor_id === m.id)).length;
          return (
            <li key={m.id} className="flex items-center gap-2 px-3 py-2">
              <StatusDot status={m.live.status} size="sm" />
              <div className="min-w-0 flex-1">
                <p className="truncate text-[13px]">{m.name}</p>
                <p className="truncate font-mono text-[10.5px] text-muted-foreground">
                  {groupCount > 0 ? `In ${groupCount} group${groupCount === 1 ? "" : "s"}` : m.target}
                </p>
              </div>
              {inTarget ? (
                <span className="text-[11px] text-muted-foreground">Added</span>
              ) : (
                <Button size="icon-sm" variant="ghost" disabled={!targetKey} onClick={() => onAdd(m)} aria-label={`Add ${m.name}`}>
                  <PlusIcon />
                </Button>
              )}
            </li>
          );
        })}
        {list.length === 0 && <li className="px-3 py-6 text-center text-xs text-muted-foreground">No monitors match</li>}
      </ul>
    </Card>
  );
}

