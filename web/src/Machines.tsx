// The computers in the sidebar. Each one is a box with its name in the header: this computer
// first, then the ones connected with their token. The box of the computer the page shows holds
// everything the sidebar has (sessions, projects, usage, the buttons at the bottom); another
// computer's header shows it instead. Under them, the computers found on the network that are not
// connected yet, and the "+" that adds one by its address.
import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useMachinesCtx } from "./useMachines";
import { machineName, type Machine } from "./machinesApi";
import { SectionAdd, SectionTitle } from "./SidebarParts";
import { IconEdit, IconMonitor, IconTrash } from "./icons";

const ICON_BUTTON =
  "flex-none p-1 rounded-md text-[#7a7a8c] hover:text-accent hover:bg-white/5 transition-colors";

// SOURCES names how a computer was found, as a short tag.
const SOURCES: Record<string, string> = {
  lan: "LAN",
  tailscale: "Tailscale",
  manual: "VPN",
};

// Chevron points down while the box is open and right while it is closed.
function Chevron({ open }: { open: boolean }) {
  return (
    <svg
      width="12"
      height="12"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2.5"
      stroke-linecap="round"
      stroke-linejoin="round"
      class={`project-chevron flex-none text-[#8a8a9a]${choose(open, "", " is-collapsed")}`}
    >
      <polyline points="6 9 12 15 18 9" />
    </svg>
  );
}

// RenameField is the name being edited in place: Enter or leaving the field saves, Escape cancels.
function RenameField({
  value,
  onSave,
  onCancel,
}: {
  value: string;
  onSave: (name: string) => void;
  onCancel: () => void;
}) {
  const [draft, setDraft] = useState(value);
  return (
    <input
      class="flex-1 min-w-0 px-2 py-1 rounded-lg bg-black/30 border border-accent/40 text-sm text-[#e8e8ea] focus:outline-hidden"
      value={draft}
      aria-label={t("Computer name")}
      data-testid="machine-rename"
      autoFocus
      onInput={(e) => setDraft((e.target as HTMLInputElement).value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          onSave(draft.trim());
        } else if (e.key === "Escape") {
          onCancel();
        }
      }}
      onBlur={() => onSave(draft.trim())}
    />
  );
}

// Status is the dot of a connected computer: green while it answers.
function Status({ online }: { online: boolean }) {
  return (
    <span
      class={`flex-none w-2 h-2 rounded-full ${choose(online, "bg-[#4ade80]", "bg-[#5a5a6a]")}`}
      title={choose(online, t("Online"), t("Offline"))}
      data-testid="machine-status"
    />
  );
}

// ForgetButton disconnects a computer, after a second click that says what it does.
function ForgetButton({ m }: { m: Machine }) {
  const { forget } = useMachinesCtx();
  const [armed, setArmed] = useState(false);
  if (armed) {
    return (
      <button
        type="button"
        class="flex-none px-2 py-0.5 rounded-md text-[11px] text-danger bg-danger/10 hover:bg-danger/20"
        data-testid="machine-forget-confirm"
        onClick={() => void forget(m.id)}
        onBlur={() => setArmed(false)}
        autoFocus
      >
        {t("Disconnect")}
      </button>
    );
  }
  return (
    <button
      type="button"
      class={ICON_BUTTON}
      aria-label={tf("Disconnect {name}", { name: machineName(m) })}
      title={t("Disconnect")}
      data-testid="machine-forget"
      onClick={() => setArmed(true)}
    >
      <IconTrash size={14} />
    </button>
  );
}

// MachineBox is one computer: its header and, when it is the one shown and open, the sidebar.
function MachineBox({
  id,
  name,
  machine,
  children,
}: {
  id: string;
  name: string;
  machine?: Machine;
  children: ComponentChildren;
}) {
  const machines = useMachinesCtx();
  const [renaming, setRenaming] = useState(false);
  const open = machines.isOpen(id);
  const shown = machines.active === id;
  // A computer that does not answer cannot be shown; the one already shown can still be folded.
  const reachable = !machine || machine.online || shown;
  const save = (value: string) => {
    setRenaming(false);
    if (value === name) {
      return;
    }
    if (machine) {
      void machines.rename(id, value);
    } else {
      void machines.renameSelf(value);
    }
  };
  return (
    <section
      class={`mx-2 mt-2 rounded-xl border ${choose(shown, "border-accent/25 bg-white/3", "border-white/6 bg-white/2")}${choose(open, " flex-1 min-h-[16rem] flex flex-col", " flex-none")}`}
      data-testid="machine"
      data-machine={id || "self"}
    >
      <div class="flex items-center gap-1.5 px-2.5 py-2">
        {renaming ? (
          <RenameField
            value={name}
            onSave={save}
            onCancel={() => setRenaming(false)}
          />
        ) : (
          <button
            type="button"
            class="flex-1 min-w-0 flex items-center gap-2 text-left disabled:opacity-50 disabled:cursor-not-allowed"
            aria-expanded={open}
            disabled={!reachable}
            title={choose(machine, machine?.address, t("This computer"))}
            onClick={() => machines.select(id)}
            data-testid="machine-toggle"
          >
            <Chevron open={open} />
            <IconMonitor size={15} class="flex-none text-accent/70" />
            <span class="truncate font-semibold text-[13px] text-[#c8c8d2]">
              {name}
            </span>
            {!machine && (
              <span class="flex-none text-[10px] text-[#7a7a8c]">
                {t("this computer")}
              </span>
            )}
            {machine && <Status online={machine.online} />}
          </button>
        )}
        {!renaming && (
          <button
            type="button"
            class={ICON_BUTTON}
            aria-label={tf("Rename {name}", { name })}
            title={t("Rename")}
            data-testid="machine-rename-button"
            onClick={() => setRenaming(true)}
          >
            <IconEdit size={14} />
          </button>
        )}
        {machine && !renaming && <ForgetButton m={machine} />}
      </div>
      {open && <div class="flex-1 min-h-0 flex flex-col">{children}</div>}
    </section>
  );
}

// FoundMachine is a computer seen on the network that is not connected yet.
function FoundMachine({ m }: { m: Machine }) {
  const { openConnect } = useMachinesCtx();
  return (
    <div
      class="flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm text-[#c8c8d2]"
      data-testid="machine-found"
    >
      <IconMonitor size={14} class="flex-none text-[#7a7a8c]" />
      <span class="flex-1 min-w-0 truncate" title={m.address}>
        {m.name}
      </span>
      <span class="flex-none text-[10px] text-[#7a7a8c]">
        {SOURCES[m.source] ?? m.source}
      </span>
      <button
        type="button"
        class="flex-none px-2 py-0.5 rounded-md text-[11px] text-accent bg-accent/10 hover:bg-accent/20"
        onClick={() => openConnect({ machine: m })}
        data-testid="machine-connect"
      >
        {t("Connect")}
      </button>
    </div>
  );
}

// OtherMachines is the computers found but not connected, the "+" that adds one by address, and a
// word when this computer cannot be reached by the others.
function OtherMachines() {
  const { view, openConnect } = useMachinesCtx();
  const found = (view?.peers ?? []).filter((p) => !p.paired);
  return (
    <section
      class="flex-none mx-2 mt-1 mb-2"
      aria-labelledby="sidebar-machines"
      data-testid="machines-other"
    >
      <SectionTitle id="sidebar-machines" label={t("Other computers")}>
        <SectionAdd
          label={t("Add a computer")}
          onClick={() => openConnect({})}
          testid="sidebar-add-machine"
        />
      </SectionTitle>
      {found.map((m) => (
        <FoundMachine key={m.id} m={m} />
      ))}
      {view && !view.self.reachable && (
        <p
          class="px-2 py-1 text-[11px] text-[#7a7a8c]"
          data-testid="machine-unreachable"
        >
          {t(
            "Other computers cannot connect to this one: it listens on this computer only. Settings → Network opens it to the local network.",
          )}
        </p>
      )}
    </section>
  );
}

// MachineList is every computer, the one shown holding children (the sidebar's own content).
export function MachineList({ children }: { children: ComponentChildren }) {
  const { view } = useMachinesCtx();
  const paired = (view?.peers ?? []).filter((p) => p.paired);
  return (
    <div
      class="flex-1 min-h-0 flex flex-col overflow-y-auto"
      data-testid="machines"
    >
      <MachineBox id="" name={view?.self.name || t("This computer")}>
        {children}
      </MachineBox>
      {paired.map((m) => (
        <MachineBox key={m.id} id={m.id} name={machineName(m)} machine={m}>
          {children}
        </MachineBox>
      ))}
      <OtherMachines />
    </div>
  );
}
