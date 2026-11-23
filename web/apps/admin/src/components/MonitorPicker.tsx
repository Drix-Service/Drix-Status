import { useState } from "react";
import { ChevronsUpDownIcon } from "lucide-react";
import { Command as CommandPrimitive } from "cmdk";
import { cn } from "@drix/ui/lib/utils";
import { Checkbox } from "@drix/ui/components/form";
import { Popover, PopoverContent, PopoverTrigger } from "@drix/ui/components/overlay";
import { StatusDot } from "@drix/ui/drix/status";
import { useMonitors } from "../lib/queries";

/** Searchable multi-select of monitors. */
export function MonitorPicker({ value, onChange, placeholder = "Choose monitors" }: { value: number[]; onChange: (ids: number[]) => void; placeholder?: string }) {
  const { data: monitors } = useMonitors();
  const [open, setOpen] = useState(false);
  const selected = new Set(value);
  const toggle = (id: number) => onChange(selected.has(id) ? value.filter((v) => v !== id) : [...value, id]);
  const names = (monitors ?? []).filter((m) => selected.has(m.id)).map((m) => m.name);

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="flex min-h-8 w-full items-center justify-between gap-2 rounded-md border border-input bg-transparent px-2.5 py-1 text-left text-[13px] shadow-xs outline-none focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/25 dark:bg-input/20"
        >
          <span className={cn("truncate", !names.length && "text-muted-foreground")}>
            {names.length ? (names.length <= 3 ? names.join(", ") : `${names.slice(0, 2).join(", ")} +${names.length - 2} more`) : placeholder}
          </span>
          <ChevronsUpDownIcon className="size-4 shrink-0 opacity-50" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-(--radix-popover-trigger-width) p-0">
        <CommandPrimitive className="flex flex-col">
          <CommandPrimitive.Input placeholder="Search monitors…" className="h-9 border-b bg-transparent px-3 text-[13px] outline-none placeholder:text-muted-foreground" />
          <CommandPrimitive.List className="max-h-64 overflow-y-auto p-1">
            <CommandPrimitive.Empty className="py-6 text-center text-xs text-muted-foreground">No monitors found.</CommandPrimitive.Empty>
            {(monitors ?? []).map((m) => (
              <CommandPrimitive.Item
                key={m.id}
                value={`${m.name} ${m.target} ${m.id}`}
                onSelect={() => toggle(m.id)}
                className="flex cursor-default items-center gap-2 rounded-md px-2 py-1.5 text-[13px] outline-none data-[selected=true]:bg-accent"
              >
                <Checkbox checked={selected.has(m.id)} tabIndex={-1} className="pointer-events-none" />
                <StatusDot status={m.live.status} size="sm" />
                <span className="truncate">{m.name}</span>
                <span className="ml-auto truncate font-mono text-[10.5px] text-muted-foreground">{m.target}</span>
              </CommandPrimitive.Item>
            ))}
          </CommandPrimitive.List>
        </CommandPrimitive>
      </PopoverContent>
    </Popover>
  );
}
