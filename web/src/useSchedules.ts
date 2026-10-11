// Scheduled tasks: the tasks the gateway fires on a cadence, the countdown to each one's next run,
// and the form that creates one.
import { useCallback, useEffect, useMemo, useState } from 'preact/hooks'
import { t, tf } from './i18n'
import { choose } from './choose'
import { clockSkewOf } from './appFormat'
import type { Api } from './prApi'
import type { ScheduledTask, SetToast } from './appTypes'
import { CLOCK_TICK_MS, HTTP_CONFLICT, JSON_HEADERS, SCHEDULES_PATH } from './appConst'

export function useSchedules(api: Api, setToast: SetToast) {
  const [showScheduledTasks, setShowScheduledTasks] = useState(false)
  const [scheduledTasks, setScheduledTasks] = useState<ScheduledTask[]>([])
  const [scheduledBusy, setScheduledBusy] = useState(false)
  // nowMs drives the countdown painted inside each task card. It ticks ONLY
  // while the panel is open: a timer that runs (and re-renders the whole app)
  // while nobody is looking at a countdown is pure cost. One second is the
  // unit the countdown itself shows at its finest.
  const [nowMs, setNowMs] = useState(() => Date.now())
  useEffect(() => {
    if (!showScheduledTasks) { return undefined }
    const id = setInterval(() => setNowMs(Date.now()), CLOCK_TICK_MS)
    return () => clearInterval(id)
  }, [showScheduledTasks])
  // The create form's working copy. Like the model switcher's draft, it is edited
  // HERE and sent only when the user presses Create: a half-typed cadence must not
  // reach the gateway.
  const [newTaskTitle, setNewTaskTitle] = useState('')
  const [newTaskText, setNewTaskText] = useState('')
  const [newTaskEvery, setNewTaskEvery] = useState('24h')
  const [newTaskKind, setNewTaskKind] = useState<'task' | 'plan'>('task')

  // loadSchedules reads the tasks the gateway holds. An empty LIST rather than an
  // error is what the gateway answers when scheduling is off, so this cannot fail
  // for that reason and does not need a branch for it.
  const loadSchedules = useCallback(async () => {
    try {
      const res = await api('/v1/schedules')
      if (!res.ok) { return }
      const data = await res.json()
      setScheduledTasks(choose(Array.isArray(data.schedules), data.schedules, []))
    } catch { /* the toast on a real failure is the caller's job */ }
  }, [])

  const createSchedule = async () => {
    if (!newTaskTitle.trim() || !newTaskText.trim()) { return }
    setScheduledBusy(true)
    try {
      const res = await api('/v1/schedules', {
        method: 'POST',
        headers: JSON_HEADERS,
        body: JSON.stringify({
          title: newTaskTitle.trim(),
          task: newTaskText.trim(),
          kind: newTaskKind,
          every: newTaskEvery.trim(),
        }),
      })
      if (!res.ok) {
        const body = await res.json().catch(() => ({}))
        setToast({ message: t('The task could not be created'), type: 'error', detail: body.error })
        return
      }
      setNewTaskTitle(''); setNewTaskText('')
      await loadSchedules()
    } finally { setScheduledBusy(false) }
  }

  const toggleSchedule = async (task: ScheduledTask) => {
    const res = await api(SCHEDULES_PATH + task.id, {
      method: 'PATCH',
      headers: JSON_HEADERS,
      body: JSON.stringify({ enabled: !task.enabled }),
    })
    if (res.ok) { await loadSchedules() }
  }

  const runScheduleNow = async (task: ScheduledTask) => {
    const res = await api(SCHEDULES_PATH + task.id + '/run', { method: 'POST' })
    if (res.status === HTTP_CONFLICT) {
      const body = await res.json().catch(() => ({}))
      setToast({ message: t('That task could not start'), type: 'error', detail: body.error })
      return
    }
    if (res.ok) {
      setToast({ message: t('Task started'), type: 'success', detail: tf('It is running in {session}.', { session: task.session_id }) })
    }
  }

  const deleteSchedule = async (task: ScheduledTask) => {
    const res = await api(SCHEDULES_PATH + task.id, { method: 'DELETE' })
    if (res.ok) { await loadSchedules() }
  }

  // The countdown is drawn between the BROWSER's clock and next_run, which the
  // GATEWAY computed. When the two machines disagree the countdown lies by the
  // difference ("4h" on a task that fires in a minute), and the gateway is the
  // one holding the schedule store: this is the smallest correction that keeps
  // the number honest. It is a memo and not state because loadSchedules is not
  // in the render path - a skew from a set of tasks a second old is fine, and
  // this way there is no second render.
  const clockSkew = useMemo(() => clockSkewOf(scheduledTasks), [scheduledTasks])

  // openScheduledTasks shows the panel with the tasks as the gateway has them now.
  const openScheduledTasks = () => {
    setShowScheduledTasks(true)
    void loadSchedules()
  }
  const closeScheduledTasks = useCallback(() => setShowScheduledTasks(false), [])

  return {
    showScheduledTasks, openScheduledTasks, closeScheduledTasks, scheduledTasks, scheduledBusy, nowMs, clockSkew,
    newTaskTitle, setNewTaskTitle, newTaskText, setNewTaskText, newTaskEvery, setNewTaskEvery, newTaskKind, setNewTaskKind,
    createSchedule, toggleSchedule, runScheduleNow, deleteSchedule,
  }
}

export type Schedules = ReturnType<typeof useSchedules>
