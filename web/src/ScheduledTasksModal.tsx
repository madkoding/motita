// The scheduled tasks: the tasks that fire into the conversation on their own, and the form that
// creates one.
import { t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { ModalBackdrop } from "./ModalBackdrop";
import { durationOf } from "./appParse";
import { taskCountdown } from "./appFormat";
import { IconClock, IconClose } from "./icons";
import type { ScheduledTask } from "./appTypes";

const TASK_BTN =
  "min-h-[44px] px-3 rounded-lg border text-xs active:scale-95 transition-transform";
const INPUT =
  "px-3 py-2 rounded-lg bg-black/30 border border-white/10 text-sm text-[#e8e8ea]";

// Countdown is the row's headline figure: the tag says what the task is SET to, this says how
// long is left. A paused task shows none - its next run is a projection, and a ticker over a task
// that will not fire would be a lie with a pulse.
function Countdown({ task }: { task: ScheduledTask }) {
  const { nowMs, clockSkew } = useApp().schedules;
  const cd = taskCountdown(task, nowMs, clockSkew);
  if (!cd) {
    return null;
  }
  return (
    <p class="text-xs text-[#9a9aaa] mt-1.5">
      {choose(cd.overdue, t("due now"), t("fires in") + " ")}
      {!cd.overdue && <span class="task-countdown">{cd.text}</span>}
    </p>
  );
}

// TaskRow is one scheduled task: when it fires next, how it went last time, and its actions.
function TaskRow({ task }: { task: ScheduledTask }) {
  const { runScheduleNow, toggleSchedule, deleteSchedule } = useApp().schedules;
  return (
    <div class="rounded-xl border border-white/10 p-3">
      <div class="flex items-center gap-2">
        <span class="flex-1 text-sm text-[#e8e8ea] truncate">{task.title}</span>
        {/* The cadence stays visible, but as a TAG: what a person wants off a row is when it
            fires NEXT, and `every` is the setting they typed when they created it. */}
        <span class="task-tag">
          {tf("every {d}", { d: durationOf(task.every).short })}
        </span>
      </div>
      <Countdown task={task} />
      {!task.enabled && (
        <p class="text-xs text-[#9a9aaa] mt-1.5">
          {tf("Paused — last {when}", {
            when: new Date(task.last_run || task.created).toLocaleString(),
          })}
        </p>
      )}
      {task.last_outcome && (
        <p class="text-xs text-[#7a7a8c] mt-1 wrap-break-word">
          {task.last_outcome}
        </p>
      )}
      <div class="flex gap-2 mt-2">
        <button
          class={TASK_BTN + " border-white/10 text-[#e8e8ea]"}
          onClick={() => void runScheduleNow(task)}
        >
          {t("Run now")}
        </button>
        <button
          class={TASK_BTN + " border-white/10 text-[#e8e8ea]"}
          onClick={() => void toggleSchedule(task)}
        >
          {choose(task.enabled, t("Pause"), t("Resume"))}
        </button>
        <button
          class={TASK_BTN + " border-danger/30 text-danger ml-auto"}
          onClick={() => void deleteSchedule(task)}
        >
          {t("Delete")}
        </button>
      </div>
    </div>
  );
}

// TaskList is what makes the feature usable: a task whose next run and last outcome a user cannot
// see is a task they cannot trust, so both are on the row and neither is behind a click.
function TaskList() {
  const { scheduledTasks } = useApp().schedules;
  return (
    <div class="tasks-list-column">
      <p class="tasks-heading">
        {tf("Scheduled ({n})", { n: scheduledTasks.length })}
      </p>
      <div class="tasks-list space-y-2 max-h-[45vh] overflow-y-auto">
        {scheduledTasks.length === 0 && (
          <p class="text-sm text-[#9a9aaa]">
            {t(
              "No scheduled tasks yet. One created here fires into the conversation you are in.",
            )}
          </p>
        )}
        {scheduledTasks.map((task) => (
          <TaskRow key={task.id} task={task} />
        ))}
      </div>
    </div>
  );
}

// TaskForm creates a task. Cadence is free text because the gateway validates it and says what
// is wrong: a fixed dropdown would be a second copy of the rule.
function TaskForm() {
  const s = useApp().schedules;
  return (
    <div class="tasks-form pt-4 border-t border-white/10 space-y-2">
      <p class="tasks-heading">{t("New task")}</p>
      <input
        class={"w-full " + INPUT}
        placeholder={t("Title")}
        value={s.newTaskTitle}
        onInput={(e) => s.setNewTaskTitle((e.target as HTMLInputElement).value)}
      />
      <textarea
        class={"w-full " + INPUT + " resize-none"}
        rows={3}
        placeholder={t("What should it do?")}
        value={s.newTaskText}
        onInput={(e) =>
          s.setNewTaskText((e.target as HTMLTextAreaElement).value)
        }
      />
      <div class="flex gap-2">
        <input
          class={"flex-1 min-w-0 " + INPUT + " font-mono"}
          placeholder="24h"
          value={s.newTaskEvery}
          onInput={(e) =>
            s.setNewTaskEvery((e.target as HTMLInputElement).value)
          }
        />
        {/* min-w-0: a flex item's automatic minimum size is its longest word, and a <select> is a
            replaced element whose text and arrow give it a real min-content width - wide enough
            ("Plan (read-only)") that the row was 3px past the form and grew a horizontal
            scrollbar over the whole left column. It keeps its intrinsic width while the column is
            wide enough and gives way before pushing anything out of the card. */}
        <select
          class={"min-w-0 " + INPUT}
          value={s.newTaskKind}
          onChange={(e) =>
            s.setNewTaskKind(
              (e.target as HTMLSelectElement).value as "task" | "plan",
            )
          }
        >
          <option value="task">{t("Task")}</option>
          <option value="plan">{t("Plan (read-only)")}</option>
        </select>
      </div>
      <button
        class="w-full min-h-[44px] px-5 rounded-xl bg-accent/20 border border-accent/30 text-[#e8e8ea] active:scale-95 transition-transform disabled:opacity-50"
        disabled={
          s.scheduledBusy || !s.newTaskTitle.trim() || !s.newTaskText.trim()
        }
        onClick={() => void s.createSchedule()}
      >
        {choose(s.scheduledBusy, t("Creating…"), t("Create"))}
      </button>
    </div>
  );
}

export function ScheduledTasksModal() {
  const { closeScheduledTasks } = useApp().schedules;
  return (
    <ModalBackdrop onClose={closeScheduledTasks}>
      <div class="tasks-panel frosted rounded-2xl border border-white/10 w-full max-w-md p-5 shadow-2xl">
        <div class="tasks-head flex items-center gap-2 mb-4">
          <IconClock size={20} class="text-accent" />
          <h2 class="text-base font-semibold">{t("Scheduled tasks")}</h2>
          <button
            class="ml-auto p-1.5 rounded-lg hover:bg-white/5"
            onClick={closeScheduledTasks}
            aria-label={t("Close")}
          >
            <IconClose size={18} />
          </button>
        </div>

        {/* On a phone this is one column, list first and the create form below it - the order
            that fits a narrow screen. On a screen with room the form moves to the LEFT of the
            list (see .tasks-body in index.css), which is the order a person works in. The DOM
            keeps the phone's order and the two columns are PLACED by the stylesheet. */}
        <div class="tasks-body">
          <TaskList />
          <TaskForm />
        </div>

        <div class="tasks-foot flex gap-2 mt-5">
          <button
            class="flex-1 min-h-[44px] px-5 rounded-xl border border-white/10 text-[#e8e8ea] active:scale-95 transition-transform"
            onClick={closeScheduledTasks}
          >
            {t("Close")}
          </button>
        </div>
      </div>
    </ModalBackdrop>
  );
}
